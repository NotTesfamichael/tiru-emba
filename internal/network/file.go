package network

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxFileSize caps what a peer can send: without a limit, a claimed size we
// blindly trust could fill a disk (whether from a bug or malice), and this
// is the one boundary in the app where "trust the network" would otherwise
// mean "trust an arbitrary other machine on the Wi-Fi."
const MaxFileSize = 200 * 1024 * 1024 // 200MB

// FileOffer is surfaced to the UI for an incoming file-transfer request.
// Respond must be called exactly once -- the sender is blocked waiting for
// it (up to fileAcceptTimeout).
type FileOffer struct {
	TransferID string
	From       string
	FileName   string // already sanitized: base name only, no path components
	FileSize   int64
	RemoteAddr string

	decision chan bool
}

// Respond accepts or denies the offer. Safe to call at most once; further
// calls are no-ops.
func (o FileOffer) Respond(accept bool) {
	select {
	case o.decision <- accept:
	default:
	}
}

// FileResult reports how an accepted incoming transfer turned out.
type FileResult struct {
	From      string
	FileName  string
	SavedPath string
	Err       error
}

func (s *Server) handleFileOffer(ctx context.Context, conn net.Conn, reader *bufio.Reader, env envelope, remote string, offerC chan<- FileOffer, resultC chan<- FileResult) {
	respond := func(accept bool, reason string) error {
		out := envelope{Type: typeFileDeny, Reason: reason}
		if accept {
			out.Type = typeFileAccept
		}
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		_ = conn.SetWriteDeadline(time.Now().Add(dialTimeout))
		_, err = conn.Write(b)
		return err
	}

	if env.FileSize < 0 {
		_ = respond(false, "invalid file size")
		return
	}
	if env.FileSize > MaxFileSize {
		_ = respond(false, fmt.Sprintf("exceeds the %s limit", HumanSize(MaxFileSize)))
		return
	}

	decision := make(chan bool, 1)
	offer := FileOffer{
		TransferID: env.TransferID,
		From:       env.From,
		FileName:   sanitizeFileName(env.FileName),
		FileSize:   env.FileSize,
		RemoteAddr: remote,
		decision:   decision,
	}

	select {
	case offerC <- offer:
	case <-ctx.Done():
		return
	}

	var accept bool
	select {
	case accept = <-decision:
	case <-time.After(fileAcceptTimeout):
		_ = respond(false, "timed out waiting for a response")
		return
	case <-ctx.Done():
		return
	}

	if !accept {
		_ = respond(false, "declined")
		return
	}
	if err := respond(true, ""); err != nil {
		return
	}

	path, err := receiveFile(conn, reader, s.fileDir, offer.FileName, env.FileSize)
	result := FileResult{From: env.From, FileName: offer.FileName, SavedPath: path, Err: err}
	select {
	case resultC <- result:
	case <-ctx.Done():
	}
}

func receiveFile(conn net.Conn, reader *bufio.Reader, dir, name string, size int64) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := uniquePath(dir, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(fileXferTimeout))
	n, copyErr := io.CopyN(f, reader, size)
	closeErr := f.Close()

	if copyErr != nil || n != size {
		os.Remove(path)
		if copyErr == nil {
			copyErr = fmt.Errorf("incomplete transfer: got %d of %d bytes", n, size)
		}
		return "", fmt.Errorf("receive file: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(path)
		return "", fmt.Errorf("save file: %w", closeErr)
	}
	return path, nil
}

// SendFile dials addr, offers path to whoever's listening, and blocks
// waiting for their accept/deny (up to fileAcceptTimeout). accepted is only
// true once the file has actually been fully streamed; reason carries the
// other side's stated reason for a deny (may be empty).
func SendFile(addr, from, path string) (accepted bool, reason string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", fmt.Errorf("network: open %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false, "", fmt.Errorf("network: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, "", fmt.Errorf("network: %s is not a regular file", path)
	}
	if info.Size() > MaxFileSize {
		return false, "", fmt.Errorf("network: %s exceeds the %s limit", path, HumanSize(MaxFileSize))
	}

	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return false, "", fmt.Errorf("network: dial %s: %w", addr, err)
	}
	defer conn.Close()

	transferID, err := randomTransferID()
	if err != nil {
		return false, "", fmt.Errorf("network: generate transfer id: %w", err)
	}

	offer := envelope{
		Type:       typeFileOffer,
		From:       from,
		FileName:   filepath.Base(path),
		FileSize:   info.Size(),
		TransferID: transferID,
	}
	b, err := json.Marshal(offer)
	if err != nil {
		return false, "", fmt.Errorf("network: marshal offer: %w", err)
	}
	b = append(b, '\n')

	_ = conn.SetWriteDeadline(time.Now().Add(dialTimeout))
	if _, err := conn.Write(b); err != nil {
		return false, "", fmt.Errorf("network: send offer: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(fileAcceptTimeout))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, "", fmt.Errorf("network: waiting for response: %w", err)
	}
	var resp envelope
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return false, "", fmt.Errorf("network: decode response: %w", err)
	}
	if resp.Type != typeFileAccept {
		return false, resp.Reason, nil
	}

	_ = conn.SetWriteDeadline(time.Now().Add(fileXferTimeout))
	if _, err := io.Copy(conn, f); err != nil {
		return false, "", fmt.Errorf("network: send file bytes: %w", err)
	}
	return true, "", nil
}

// sanitizeFileName strips any path components a peer might have claimed
// (accidentally or not) so a received file can never be written outside dir
// -- e.g. a claimed name of "../../.ssh/authorized_keys" becomes just
// "authorized_keys".
func sanitizeFileName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Sprintf("received_file_%d", time.Now().UnixNano())
	}
	return name
}

// uniquePath appends " (1)", " (2)", ... before the extension if name
// already exists in dir, matching the familiar browser-download convention.
func uniquePath(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// HumanSize renders a byte count as e.g. "2.3 MB".
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func randomTransferID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
