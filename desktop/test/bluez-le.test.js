// Tests for the BlueZ LE connect (src/bluez-le.js): the D-Bus calls busctl is given.
// Run with: npm test (plain node:test, no Electron needed).
'use strict';

const assert = require('node:assert/strict');
const { test } = require('node:test');

const { connectArgs, devicePath } = require('../src/bluez-le');

test('devicePath is BlueZ object path of the address', () => {
  assert.equal(devicePath('/org/bluez/hci0', 'f4:4c:26:17:17:ff'), '/org/bluez/hci0/dev_F4_4C_26_17_17_FF');
});

test('connectArgs asks ConnectDevice for the public address', () => {
  assert.deepEqual(connectArgs('/org/bluez/hci0', 'f4:4c:26:17:17:ff'), [
    'call', 'org.bluez', '/org/bluez/hci0', 'org.bluez.Adapter1', 'ConnectDevice', 'a{sv}', '2',
    'Address', 's', 'F4:4C:26:17:17:FF', 'AddressType', 's', 'public',
  ]);
});
