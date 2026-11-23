package app

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
)

func TestVisibleTailscalePeerDoesNotMakeClosedSSHHealthy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	r := diagnose(context.Background(), DownloadRequest{Agent: nodeclient.Config{URL: "https://" + addr}, SSH: transport.Config{Address: addr}}, func(context.Context) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Peer":{"fixture":{"TailscaleIPs":["127.0.0.1"],"Online":true,"Active":false,"Relay":"sfo"}}}`), nil
	})
	states := map[string]string{}
	for _, l := range r.Layers {
		states[l.Name] = l.State
	}
	if states["tailscale"] != "ok" || states["ssh_port"] != "failed" || states["ssh_identity"] != "failed" || r.TransferVerified || r.Path != "unknown" || r.ObservedAt == "" {
		t.Fatal(r)
	}
	// Never contact a URL carrying credentials or echo its userinfo in results.
	r = diagnose(context.Background(), DownloadRequest{Agent: nodeclient.Config{URL: "https://secret@localhost"}, SSH: transport.Config{Address: addr}}, func(context.Context) ([]byte, error) { return nil, context.Canceled })
	for _, l := range r.Layers {
		if strings.Contains(l.Detail, "secret") {
			t.Fatal("credential leaked", r)
		}
	}
}
