// Package ble implements the BlueZ GATT peripheral server for the now-playing
// companion protocol. It is a thin glue layer: all business logic lives in
// internal/nowplaying. Requires BlueZ ≥ 5.50 with the D-Bus GATT API and a
// powered Bluetooth adapter (e.g. `bluetoothctl power on`).
package ble

import (
	"context"
	"log"
	"time"

	"tinygo.org/x/bluetooth"

	"rover-mems-agent/internal/headunit"
	"rover-mems-agent/internal/navigation"
	"rover-mems-agent/internal/notification"
	"rover-mems-agent/internal/nowplaying"
)

// Run starts the GATT peripheral, advertises the companion service (now-playing
// plus navigation, alert, and remote-control characteristics), and blocks until
// ctx is cancelled. It returns nil on clean shutdown and an error if the adapter
// cannot be enabled or the service cannot be registered.
func Run(ctx context.Context, store *nowplaying.Store, navStore *navigation.Store, notifStore *notification.Store, huStore *headunit.Store, deviceName string) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return err
	}

	serviceUUID, err := bluetooth.ParseUUID(nowplaying.ServiceUUID)
	if err != nil {
		return err
	}
	metaUUID, err := bluetooth.ParseUUID(nowplaying.MetadataCharUUID)
	if err != nil {
		return err
	}
	artCtrlUUID, err := bluetooth.ParseUUID(nowplaying.ArtControlCharUUID)
	if err != nil {
		return err
	}
	artDataUUID, err := bluetooth.ParseUUID(nowplaying.ArtDataCharUUID)
	if err != nil {
		return err
	}
	navUUID, err := bluetooth.ParseUUID(navigation.NavCharUUID)
	if err != nil {
		return err
	}
	navIconCtrlUUID, err := bluetooth.ParseUUID(navigation.IconControlCharUUID)
	if err != nil {
		return err
	}
	navIconDataUUID, err := bluetooth.ParseUUID(navigation.IconDataCharUUID)
	if err != nil {
		return err
	}
	alertUUID, err := bluetooth.ParseUUID(notification.AlertCharUUID)
	if err != nil {
		return err
	}
	huCmdUUID, err := bluetooth.ParseUUID(headunit.CommandCharUUID)
	if err != nil {
		return err
	}
	huCatalogUUID, err := bluetooth.ParseUUID(headunit.CatalogCharUUID)
	if err != nil {
		return err
	}

	// catalogChar is populated by AddService; we push catalog notifications
	// through it from the goroutine below.
	var catalogChar bluetooth.Characteristic

	svc := bluetooth.Service{
		UUID: serviceUUID,
		Characteristics: []bluetooth.CharacteristicConfig{
			{
				UUID:  metaUUID,
				Flags: bluetooth.CharacteristicWritePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := store.HandleMetadata(value); err != nil {
						log.Printf("ble: HandleMetadata: %v", err)
					}
				},
			},
			{
				UUID:  artCtrlUUID,
				Flags: bluetooth.CharacteristicWritePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := store.HandleArtControl(value); err != nil {
						log.Printf("ble: HandleArtControl: %v", err)
					}
				},
			},
			{
				UUID:  artDataUUID,
				Flags: bluetooth.CharacteristicWriteWithoutResponsePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := store.HandleArtChunk(value); err != nil {
						log.Printf("ble: HandleArtChunk: %v", err)
					}
				},
			},
			{
				UUID:  navUUID,
				Flags: bluetooth.CharacteristicWritePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := navStore.HandleNavigation(value); err != nil {
						log.Printf("ble: HandleNavigation: %v", err)
					}
				},
			},
			{
				UUID:  navIconCtrlUUID,
				Flags: bluetooth.CharacteristicWritePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := navStore.HandleIconControl(value); err != nil {
						log.Printf("ble: HandleIconControl: %v", err)
					}
				},
			},
			{
				UUID:  navIconDataUUID,
				Flags: bluetooth.CharacteristicWriteWithoutResponsePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := navStore.HandleIconChunk(value); err != nil {
						log.Printf("ble: HandleIconChunk: %v", err)
					}
				},
			},
			{
				UUID:  alertUUID,
				Flags: bluetooth.CharacteristicWritePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := notifStore.HandleAlert(value); err != nil {
						log.Printf("ble: HandleAlert: %v", err)
					}
				},
			},
			{
				UUID:  huCmdUUID,
				Flags: bluetooth.CharacteristicWritePermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if err := huStore.HandleCommand(value); err != nil {
						log.Printf("ble: HandleCommand: %v", err)
					} else {
						log.Printf("ble: head-unit command relayed: %s", value)
					}
				},
			},
			{
				Handle: &catalogChar,
				UUID:   huCatalogUUID,
				Flags:  bluetooth.CharacteristicNotifyPermission,
			},
		},
	}

	if err := adapter.AddService(&svc); err != nil {
		return err
	}

	// Relay catalog updates from the frontend (via the store) to the phone as
	// fragmented notifications.
	go runCatalogNotifier(ctx, huStore, &catalogChar)

	adv := adapter.DefaultAdvertisement()
	if err := adv.Configure(bluetooth.AdvertisementOptions{
		LocalName:    deviceName,
		ServiceUUIDs: []bluetooth.UUID{serviceUUID},
	}); err != nil {
		return err
	}
	if err := adv.Start(); err != nil {
		return err
	}

	<-ctx.Done()

	if err := adv.Stop(); err != nil {
		log.Printf("ble: stop advertising: %v", err)
	}
	return nil
}

// interFragmentDelay paces catalog notifications so BlueZ does not coalesce
// rapid value-property updates into a single notification.
const interFragmentDelay = 3 * time.Millisecond

// runCatalogNotifier subscribes to catalog updates and writes each as ordered,
// fragmented BLE notifications on the catalog characteristic until ctx is done.
func runCatalogNotifier(ctx context.Context, huStore *headunit.Store, catalogChar *bluetooth.Characteristic) {
	ch, unsub := huStore.SubscribeCatalog()
	defer unsub()
	for {
		select {
		case <-ctx.Done():
			return
		case catalog, ok := <-ch:
			if !ok {
				return
			}
			frames := headunit.BuildFrames(catalog, headunit.MaxFragmentPayload)
			sent := 0
			for _, frame := range frames {
				if _, err := catalogChar.Write(frame); err != nil {
					log.Printf("ble: catalog notify: %v", err)
					break
				}
				sent++
				select {
				case <-ctx.Done():
					return
				case <-time.After(interFragmentDelay):
				}
			}
			log.Printf("ble: catalog notified: %d bytes in %d/%d frames", len(catalog), sent, len(frames))
		}
	}
}
