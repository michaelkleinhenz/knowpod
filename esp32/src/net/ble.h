#pragma once

#include <Arduino.h>

// Bluetooth LE transfer of recordings to the knowpod desktop or mobile app, for
// when no known Wi-Fi network is in range: the app reads the recordings that
// wait for an upload and relays them to the backend with the device token
// (docs/ble-transfer.md). Wi-Fi uploads stay the first choice; the worker asks
// for Bluetooth while uploads wait and Wi-Fi can't be reached, and (slowly) while
// an app is paired and there is no Wi-Fi.
//
// Only apps paired with the device may connect: pairing uses LE Secure
// Connections with a passkey that the device shows while the user has pairing
// switched on (Settings > Bluetooth), and every request needs the encrypted,
// authenticated link. The stack runs only while it is offered, paired for, or an
// app is connected, so it costs no power otherwise.
//
// All functions are thread-safe.

void ble_begin();

// What the recorder offers a paired app while it has no Wi-Fi: nothing (Wi-Fi works), IDLE
// (nothing waits: slow advertising, so the app's "Copy" still finds the recorder) or WAITING
// (recordings wait for an upload: fast advertising, and the device stays awake a while).
enum BleOffer { BLE_OFFER_NONE, BLE_OFFER_IDLE, BLE_OFFER_WAITING };
void ble_offer(BleOffer offer);
void ble_pause(bool paused);        // while recording (drops a connection)

bool ble_running();                 // the stack is on (advertising or connected)
bool ble_offering();                // advertising recordings for the app; keeps the device awake a while
bool ble_connected();               // an app is connected: keep the device awake
bool ble_sending();                 // a recording is being read right now

void ble_start_pairing();           // accept a new app for a few minutes
void ble_stop_pairing();
bool ble_pairing();
String ble_passkey();               // the code to enter in the app, "" until it asks
bool ble_paired_now();              // an app was paired since ble_start_pairing()
int ble_paired_count();             // apps paired with the device
void ble_forget_all();              // unpair all apps

String ble_name();                  // advertised name, "knowpod-1A2B"
String ble_status();                // for the settings screen
String ble_status_short();          // for the status bar, "" when idle
uint32_t ble_generation();          // changes whenever the status changes
