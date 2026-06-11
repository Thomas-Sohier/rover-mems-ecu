// Package ble implements the BlueZ GATT peripheral server for the now-playing
// companion protocol. It is a thin glue layer: all business logic lives in
// internal/nowplaying. Requires BlueZ ≥ 5.50 with the D-Bus GATT API and a
// powered Bluetooth adapter (e.g. `bluetoothctl power on`).
package ble

import (
	"context"
	"log"

	"tinygo.org/x/bluetooth"

	"rover-mems-agent/internal/nowplaying"
)

// Run starts the GATT peripheral, advertises the now-playing service, and
// blocks until ctx is cancelled. It returns nil on clean shutdown and an error
// if the adapter cannot be enabled or the service cannot be registered.
func Run(ctx context.Context, store *nowplaying.Store, deviceName string) error {
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
		},
	}

	if err := adapter.AddService(&svc); err != nil {
		return err
	}

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
