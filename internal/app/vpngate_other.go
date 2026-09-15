//go:build !linux

package app

import (
	"context"
	"fmt"
)

// On Windows and macOS the VPNGate egress cannot be built: SoftEther's Linux
// TAP adapter, dhclient and policy routing simply do not exist there. The stub
// fails closed so a managed outbound is never silently treated as plain DIRECT.
type vpnGateStubLink struct{}

func newVPNGateLink(*CoreManager) vpnGateLink { return vpnGateStubLink{} }

func (vpnGateStubLink) Connect(context.Context, int, VPNGateNode) error {
	return fmt.Errorf("VPNGate 只支持 Linux 服务器")
}

func (vpnGateStubLink) Verify(context.Context, int) error {
	return fmt.Errorf("VPNGate 只支持 Linux 服务器")
}

func (vpnGateStubLink) Disconnect(context.Context, int) error { return nil }
func (vpnGateStubLink) Cleanup(context.Context, int) error    { return nil }

func (m *CoreManager) stopVPNGateService() {}
