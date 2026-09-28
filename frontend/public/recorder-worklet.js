// Audio worklet of the voice recorder (src/lib/wavRecorder.ts): hands each block of the
// microphone's audio, mixed down to mono, to the page. It outputs silence.
class KnowpodRecorder extends AudioWorkletProcessor {
  process(inputs) {
    const input = inputs[0];
    if (input && input.length > 0 && input[0].length > 0) {
      const n = input[0].length;
      const mono = new Float32Array(n);
      for (const channel of input) {
        for (let i = 0; i < n; i++) mono[i] += channel[i] / input.length;
      }
      this.port.postMessage(mono, [mono.buffer]);
    }
    return true;
  }
}

registerProcessor('knowpod-recorder', KnowpodRecorder);
