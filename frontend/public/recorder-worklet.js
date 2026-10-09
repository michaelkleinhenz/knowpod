// Audio worklets of the voice recorder (src/lib/wavRecorder.ts).
//
// knowpod-recorder hands each block of the recorded audio to the page: an array with one
// Float32Array per channel of the recording (processorOptions.channels, 1 when not given:
// the input mixed down to mono). It outputs silence.
class KnowpodRecorder extends AudioWorkletProcessor {
  constructor(options) {
    super();
    this.channels = (options && options.processorOptions && options.processorOptions.channels) || 1;
  }

  process(inputs) {
    const input = inputs[0];
    if (input && input.length > 0 && input[0].length > 0) {
      const n = input[0].length;
      let out;
      if (this.channels === 1) {
        const mono = new Float32Array(n);
        for (const channel of input) {
          for (let i = 0; i < n; i++) mono[i] += channel[i] / input.length;
        }
        out = [mono];
      } else {
        out = [];
        for (let c = 0; c < this.channels; c++) out.push(input[c] ? input[c].slice() : new Float32Array(n));
      }
      this.port.postMessage(out, out.map((a) => a.buffer));
    }
    return true;
  }
}

registerProcessor('knowpod-recorder', KnowpodRecorder);

// knowpod-pcm-source plays mono samples the page posts to it (Float32Array chunks at the
// context's sample rate), e.g. what the computer plays, recorded by the desktop app
// (src/lib/systemAudio.ts). It keeps a little in store against jitter: it starts once
// 60 ms are there, plays silence when it runs dry and drops the oldest when over half a second
// piles up, so it never lags behind.
class KnowpodPcmSource extends AudioWorkletProcessor {
  constructor() {
    super();
    this.queue = [];
    this.offset = 0; // into queue[0]
    this.stored = 0;
    this.playing = false;
    this.port.onmessage = (e) => {
      const chunk = e.data;
      if (!(chunk instanceof Float32Array) || chunk.length === 0) return;
      this.queue.push(chunk);
      this.stored += chunk.length;
      const max = sampleRate / 2;
      while (this.stored > max && this.queue.length > 1) {
        const dropped = this.queue.shift();
        this.stored -= dropped.length - this.offset;
        this.offset = 0;
      }
    };
  }

  process(_inputs, outputs) {
    const out = outputs[0] && outputs[0][0];
    if (!out) return true;
    if (!this.playing && this.stored >= sampleRate * 0.06) this.playing = true;
    if (!this.playing) return true;
    let i = 0;
    while (i < out.length && this.queue.length > 0) {
      const head = this.queue[0];
      const n = Math.min(out.length - i, head.length - this.offset);
      out.set(head.subarray(this.offset, this.offset + n), i);
      i += n;
      this.offset += n;
      this.stored -= n;
      if (this.offset >= head.length) {
        this.queue.shift();
        this.offset = 0;
      }
    }
    if (i < out.length) this.playing = false; // ran dry: wait for some to come again
    for (let c = 1; c < outputs[0].length; c++) outputs[0][c].set(out);
    return true;
  }
}

registerProcessor('knowpod-pcm-source', KnowpodPcmSource);
