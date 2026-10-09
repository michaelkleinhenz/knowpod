// Tests for reading the outputs out of pactl (src/system-audio.js).
// Run with: npm test (plain node:test, no Electron needed).
'use strict';

const assert = require('node:assert/strict');
const { test } = require('node:test');

const { loopback, parseSinks, supported } = require('../src/system-audio');

test('parseSinks reads names and descriptions of pactl list sinks', () => {
  const text = `Sink #52
	State: RUNNING
	Name: alsa_output.pci-0000_00_1f.3.analog-stereo
	Description: Built-in Audio Analog Stereo
	Driver: PipeWire
	Properties:
		device.description = "Built-in Audio"
		node.name = "alsa_output.pci-0000_00_1f.3.analog-stereo"

Sink #61
	State: SUSPENDED
	Name: bluez_output.AA_BB_CC_DD_EE_FF.1
	Description: Headset
	Monitor Source: bluez_output.AA_BB_CC_DD_EE_FF.1.monitor
`;
  assert.deepEqual(parseSinks(text), [
    { name: 'alsa_output.pci-0000_00_1f.3.analog-stereo', description: 'Built-in Audio Analog Stereo' },
    { name: 'bluez_output.AA_BB_CC_DD_EE_FF.1', description: 'Headset' },
  ]);
});

test('parseSinks names an output without description by its name', () => {
  assert.deepEqual(parseSinks('Sink #1\n\tName: null\n'), [{ name: 'null', description: 'null' }]);
  assert.deepEqual(parseSinks(''), []);
});

test('loopback: Chromium records on Windows and macOS 13 and later, parec on Linux', () => {
  assert.equal(loopback('win32', '10.0.22631'), true);
  assert.equal(loopback('darwin', '23.2.0'), true);
  assert.equal(loopback('darwin', '22.1.0'), true);
  assert.equal(loopback('darwin', '21.6.0'), false);
  assert.equal(loopback('linux', '6.8.0'), false);
  assert.equal(supported('linux', '6.8.0'), true);
  assert.equal(supported('darwin', '21.6.0'), false);
  assert.equal(supported('freebsd', '14.0'), false);
});
