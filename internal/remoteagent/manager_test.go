package remoteagent

import (
	"context"
	"github.com/DVE-FCT/HopSend/internal/nodeclient"
	transport "github.com/DVE-FCT/HopSend/internal/transport/sftp"
	"testing"
)

func TestPreparationRejectsMismatchedDeviceBeforeConnecting(t *testing.T) {
	m := NewManager(context.Background())
	defer m.Close()
	r, err := m.Prepare(context.Background(), Request{ConfigPath: "C:/config.json", SSH: transport.Config{Address: "127.0.0.1:22"}, Agent: nodeclient.Config{URL: "https://different-device.invalid:9443"}})
	if err == nil || r.Ready || len(r.Steps) != 1 || r.Steps[0].Name != "configuration" {
		t.Fatal(r, err)
	}
}
