"""Tests for pocket-keybind's offline logic (no Bluetooth needed)."""
import sys
import types

import pytest

# pocket.py imports bleak at load; stub it only if it isn't installed, so the real test
# environment still uses the real library.
try:  # pragma: no cover - depends on the environment
    import bleak  # noqa: F401
except ImportError:  # pragma: no cover
    stub = types.ModuleType("bleak")
    stub.BleakClient = object
    stub.BleakScanner = object
    sys.modules["bleak"] = stub

from pocket_wifi_probe import keybind


def test_parse_steps_accepts_known_kinds():
    assert keybind.parse_steps("none,key,fresh,key") == [
        ("none", None), ("key", None), ("fresh", None), ("key", None)]
    assert keybind.parse_steps(" other:ABC123 ") == [("other", "ABC123")]


@pytest.mark.parametrize("bad", ["", "key:x", "nonsense", "other:", "none:1"])
def test_parse_steps_rejects_bad(bad):
    with pytest.raises(Exception):
        keybind.parse_steps(bad)


def test_fresh_key_shape():
    keys = {keybind.fresh_key() for _ in range(50)}
    assert len(keys) == 50  # random, so collisions are vanishingly unlikely
    for k in keys:
        assert len(k) == 16 and all(c in keybind.KEY_CHARS for c in k)


def test_redactor_hides_keys_and_prefixes():
    red = keybind.redactor(["SECRETKEY1234567", "SECRETKE", "", None])
    assert red("APP&SK&SECRETKEY1234567 then SECRETKE") == "APP&SK&<key> then <key>"


def _results(steps, answers):
    return [{"step": s, "connected": True, "answered_after_key": a}
            for (s, _), a in zip(steps, answers)]


def test_interpret_first_key_wins():
    steps = keybind.parse_steps("none,key,fresh,key")
    notes = " ".join(keybind.interpret(steps, _results(steps, [False, True, False, True])))
    assert "a key is required" in notes
    assert "command-line key unlocked" in notes
    assert "first-key-wins" in notes


def test_interpret_any_key_works():
    steps = keybind.parse_steps("none,key,fresh,key")
    notes = " ".join(keybind.interpret(steps, _results(steps, [False, True, True, True])))
    assert "accepts any key" in notes
    assert "first-key-wins" not in notes


def test_interpret_not_locked():
    steps = keybind.parse_steps("none,key,fresh,key")
    notes = " ".join(keybind.interpret(steps, _results(steps, [True, True, True, True])))
    assert "isn't locked at all" in notes
