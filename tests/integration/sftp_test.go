package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DVE-FCT/HopSend/internal/cli"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type readFixture struct {
	root    *os.Root
	delay   time.Duration
	started chan struct{}
	once    sync.Once
}

func (f *readFixture) Fileread(request *pkgsftp.Request) (io.ReaderAt, error) {
	path := strings.TrimPrefix(request.Filepath, "/")
	rootPrefix := strings.TrimPrefix(filepath.ToSlash(f.root.Name()), "/") + "/"
	path = strings.TrimPrefix(path, rootPrefix)
	if !fs.ValidPath(path) || strings.ContainsAny(path, `\:`) {
		return nil, fs.ErrPermission
	}
	file, err := f.root.Open(path)
	if err != nil {
		return nil, err
	}
	return &slowFile{File: file, fixture: f}, nil
}

type slowFile struct {
	*os.File
	fixture *readFixture
}

func (f *slowFile) ReadAt(p []byte, offset int64) (int, error) {
	f.fixture.once.Do(func() { close(f.fixture.started) })
	if f.fixture.delay > 0 {
		time.Sleep(f.fixture.delay)
	}
	return f.File.ReadAt(p, offset)
}

func newSigner(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	signer, err := ssh.ParsePrivateKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return signer, encoded
}

func startSFTP(t *testing.T, delay time.Duration) (transport.Config, []byte, <-chan struct{}) {
	cfg, data, started, _ := startSFTPControlled(t, delay)
	return cfg, data, started
}

func startSFTPControlled(t *testing.T, delay time.Duration) (transport.Config, []byte, <-chan struct{}, func()) {
	t.Helper()
	dir := t.TempDir()
	data := bytes.Repeat([]byte("HopSend-中文-0123456789"), 131072)
	if err := os.WriteFile(filepath.Join(dir, "sample.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &readFixture{root: root, delay: delay, started: make(chan struct{})}
	host, _ := newSigner(t)
	clientKey, clientPEM := newSigner(t)
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if metadata.User() == "fixture" && bytes.Equal(key.Marshal(), clientKey.PublicKey().Marshal()) {
			return &ssh.Permissions{}, nil
		}
		return nil, errors.New("denied")
	}}
	// Reproduce Windows OpenSSH offering another preferred host algorithm while
	// the client has verified only ED25519. Both plain and hashed trust records
	// must continue to select the verified identity, not silently add new keys.
	extraPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	extraHost, err := ssh.NewSignerFromKey(extraPrivate)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig.AddHostKey(extraHost)
	serverConfig.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var active []net.Conn
	closing := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			select {
			case <-closing:
				_ = conn.Close()
				mu.Unlock()
				return
			default:
			}
			active = append(active, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				sshConn, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
				if err != nil {
					return
				}
				defer sshConn.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						_ = incoming.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						continue
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer channel.Close()
						for request := range requests {
							var subsystem struct{ Name string }
							ok := request.Type == "subsystem" && ssh.Unmarshal(request.Payload, &subsystem) == nil && subsystem.Name == "sftp"
							_ = request.Reply(ok, nil)
							if ok {
								server := pkgsftp.NewRequestServer(channel, pkgsftp.Handlers{FileGet: fixture})
								_ = server.Serve()
								_ = server.Close()
								return
							}
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		close(closing)
		_ = listener.Close()
		mu.Lock()
		for _, conn := range active {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
		_ = root.Close()
	})
	keyPath := filepath.Join(dir, "client.key")
	knownPath := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(keyPath, clientPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownPath, []byte(knownhosts.Line([]string{knownhosts.Normalize(listener.Addr().String())}, host.PublicKey())+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	drop := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range active {
			_ = conn.Close()
		}
	}
	return transport.Config{Address: listener.Addr().String(), User: "fixture", PrivateKeyPath: keyPath, KnownHostsPath: knownPath, Timeout: 2 * time.Second}, data, fixture.started, drop
}

func TestSFTPRealRangeAndCLI(t *testing.T) {
	cfg, source, _ := startSFTP(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := transport.Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	const offset, length = 12345, 1048576
	var result bytes.Buffer
	n, err := client.ReadRange(ctx, "/sample.bin", offset, length, &result)
	if err != nil || n != length || !bytes.Equal(result.Bytes(), source[offset:offset+length]) {
		t.Fatalf("range: n=%d err=%v", n, err)
	}
	expected := sha256.Sum256(source[offset : offset+length])
	args := []string{"sftp-read", "--address", cfg.Address, "--user", cfg.User, "--key", cfg.PrivateKeyPath, "--known-hosts", cfg.KnownHostsPath, "--remote", "/sample.bin", "--offset", "12345", "--length", "1048576", "--sha256", hex.EncodeToString(expected[:])}
	var out, stderr bytes.Buffer
	if code := cli.Run(ctx, args, &out, &stderr); code != 0 {
		t.Fatalf("CLI %d: %s", code, stderr.String())
	}
	if !strings.Contains(out.String(), `"expected_hash_verified": true`) {
		t.Fatal(out.String())
	}
	t.Log("real TCP SSH/SFTP range and CLI expected SHA-256 match")
}

func TestSFTPSelectsHashedTrustedHostWithMultipleAlgorithms(t *testing.T) {
	cfg, source, _ := startSFTP(t, 0)
	data, err := os.ReadFile(cfg.KnownHostsPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	fields[0] = knownhosts.HashHostname(knownhosts.Normalize(cfg.Address))
	if err := os.WriteFile(cfg.KnownHostsPath, []byte(strings.Join(fields, " ")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := transport.Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var output bytes.Buffer
	if _, err := c.ReadRange(ctx, "/sample.bin", 17, 8192, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), source[17:17+8192]) {
		t.Fatal("hashed trusted host returned incorrect range")
	}
}

func TestSFTPRejectsHostAndUserKeys(t *testing.T) {
	cfg, _, _ := startSFTP(t, 0)
	wrong, wrongPEM := newSigner(t)
	t.Run("changed host", func(t *testing.T) {
		bad := cfg
		bad.KnownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
		_ = os.WriteFile(bad.KnownHostsPath, []byte(knownhosts.Line([]string{knownhosts.Normalize(cfg.Address)}, wrong.PublicKey())+"\n"), 0600)
		if c, err := transport.Dial(context.Background(), bad); err == nil {
			c.Close()
			t.Fatal("wrong host trusted")
		}
	})
	t.Run("unknown host", func(t *testing.T) {
		bad := cfg
		bad.KnownHostsPath = filepath.Join(t.TempDir(), "known_hosts")
		_ = os.WriteFile(bad.KnownHostsPath, nil, 0600)
		if c, err := transport.Dial(context.Background(), bad); err == nil {
			c.Close()
			t.Fatal("unknown host trusted")
		}
	})
	t.Run("wrong user key", func(t *testing.T) {
		bad := cfg
		bad.PrivateKeyPath = filepath.Join(t.TempDir(), "wrong.key")
		_ = os.WriteFile(bad.PrivateKeyPath, wrongPEM, 0600)
		if c, err := transport.Dial(context.Background(), bad); err == nil {
			c.Close()
			t.Fatal("wrong user authenticated")
		}
	})
}

func TestSFTPRejectsTruncatedAndInvalidRanges(t *testing.T) {
	cfg, data, _ := startSFTP(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := transport.Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.ReadRange(ctx, "/sample.bin", int64(len(data)-10), 20, io.Discard); err == nil {
		t.Fatal("truncated range accepted")
	}
	for _, r := range [][2]int64{{-1, 1}, {0, 0}, {1<<63 - 2, 4}} {
		if _, err = c.ReadRange(ctx, "/sample.bin", r[0], r[1], io.Discard); err == nil {
			t.Fatal("invalid range accepted", r)
		}
	}
}

func TestSFTPInFlightCancellation(t *testing.T) {
	cfg, data, started := startSFTP(t, 100*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := transport.Dial(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.ReadRange(ctx, "/sample.bin", 0, int64(len(data)), io.Discard); done <- err }()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Fatal("read did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not unblock range read")
	}
}

func TestSFTPHandshakeDeadline(t *testing.T) {
	cfg, _, _ := startSFTP(t, 0)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	accepted := make(chan struct{})
	go func() {
		conn, err := l.Accept()
		if err == nil {
			defer conn.Close()
			close(accepted)
			<-done
		}
	}()
	defer close(done)
	cfg.Address = l.Addr().String()
	trusted, err := os.ReadFile(cfg.KnownHostsPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(trusted))
	fields[0] = knownhosts.Normalize(cfg.Address)
	if err := os.WriteFile(cfg.KnownHostsPath, []byte(strings.Join(fields, " ")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	c, err := transport.Dial(ctx, cfg)
	if err == nil {
		c.Close()
		t.Fatal("stalled handshake accepted")
	}
	select {
	case <-accepted:
	default:
		t.Fatal("deadline test never reached the TCP server")
	}
	var networkError net.Error
	if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &networkError) && networkError.Timeout()) {
		t.Fatal("expected a handshake deadline, got", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("handshake timeout did not stop I/O")
	}
}
