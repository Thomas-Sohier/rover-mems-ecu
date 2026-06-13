// Package bluetooth registers a BlueZ pairing agent on the D-Bus system bus so
// that inbound Bluetooth connections are auto-accepted without user interaction.
// It also ensures the adapter is Powered, Pairable, and Discoverable.
//
// This package never initiates outbound connections or scans; it is strictly an
// inbound-only accept agent.
package bluetooth

import (
	"log"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	agentPath         = dbus.ObjectPath("/carpi/agent")
	agentIface        = "org.bluez.Agent1"
	agentManagerDest  = "org.bluez"
	agentManagerPath  = dbus.ObjectPath("/org/bluez")
	agentManagerIface = "org.bluez.AgentManager1"
	adapterPath       = dbus.ObjectPath("/org/bluez/hci0")
	adapterIface      = "org.bluez.Adapter1"
	propIface         = "org.freedesktop.DBus.Properties"
)

// agent implements org.bluez.Agent1 — all methods auto-accept.
type agent struct{}

func (a *agent) Release() *dbus.Error                                             { return nil }
func (a *agent) RequestPinCode(_ dbus.ObjectPath) (string, *dbus.Error)           { return "0000", nil }
func (a *agent) DisplayPinCode(_ dbus.ObjectPath, _ string) *dbus.Error           { return nil }
func (a *agent) RequestPasskey(_ dbus.ObjectPath) (uint32, *dbus.Error)           { return 0, nil }
func (a *agent) DisplayPasskey(_ dbus.ObjectPath, _ uint32, _ uint16) *dbus.Error { return nil }
func (a *agent) RequestConfirmation(_ dbus.ObjectPath, _ uint32) *dbus.Error      { return nil }
func (a *agent) RequestAuthorization(_ dbus.ObjectPath) *dbus.Error               { return nil }
func (a *agent) AuthorizeService(_ dbus.ObjectPath, _ string) *dbus.Error         { return nil }
func (a *agent) Cancel() *dbus.Error                                              { return nil }

// SetupAgent connects to the D-Bus system bus, configures the Bluetooth adapter
// (Powered, Pairable, Discoverable), exports the pairing agent, registers it
// with BlueZ, and makes it the default agent. It returns any error encountered;
// the caller should log and continue rather than crashing the whole server.
func SetupAgent() error {
	conn, err := dbus.SystemBus()
	if err != nil {
		return err
	}

	if err := ensureAdapterReady(conn); err != nil {
		// Non-fatal: adapter may already be configured via main.conf.
		log.Printf("bluetooth: adapter setup: %v", err)
	}

	// Export the agent object.
	a := &agent{}
	if err := conn.ExportAll(a, agentPath, agentIface); err != nil {
		return err
	}
	if err := conn.Export(
		introspect.NewIntrospectable(&introspect.Node{
			Interfaces: []introspect.Interface{
				{Name: agentIface},
			},
		}),
		agentPath,
		"org.freedesktop.DBus.Introspectable",
	); err != nil {
		return err
	}

	mgr := conn.Object(agentManagerDest, agentManagerPath)

	if err := mgr.Call(agentManagerIface+".RegisterAgent", 0, agentPath, "NoInputNoOutput").Err; err != nil {
		return err
	}
	if err := mgr.Call(agentManagerIface+".RequestDefaultAgent", 0, agentPath).Err; err != nil {
		return err
	}

	log.Printf("bluetooth: pairing agent registered at %s", agentPath)
	return nil
}

// ensureAdapterReady sets Powered, Pairable, and Discoverable on hci0.
func ensureAdapterReady(conn *dbus.Conn) error {
	adapter := conn.Object(agentManagerDest, adapterPath)
	props := map[string]interface{}{
		"Powered":      true,
		"Pairable":     true,
		"Discoverable": true,
	}
	for k, v := range props {
		if err := adapter.Call(propIface+".Set", 0, adapterIface, k, dbus.MakeVariant(v)).Err; err != nil {
			return err
		}
	}
	return nil
}
