// WavRecorder records the microphone as 16 kHz mono 16-bit WAV, the format the server
// archives as FLAC and transcribes (speech needs no more). The audio is taken with an audio
// worklet (public/recorder-worklet.js) and averaged down to 16 kHz as it comes in, so a long
// memo takes about 2 MB of memory per minute.
//
// Phones may suspend the recording when the screen is locked: Android keeps capturing while
// the microphone is in use, but iOS suspends the page (the audio context reports
// "interrupted" and the microphone may be muted or ended). The recorder notes such an
// interruption and revive() picks the recording up again when the page is back.

export const SAMPLE_RATE = 16_000;

export class RecorderUnsupportedError extends Error {}

export class WavRecorder {
  private chunks: Int16Array<ArrayBuffer>[] = [];
  private samples = 0;
  private paused = false;
  // Averaging down: the input samples summed for the next output sample, and how far into it.
  private sum = 0;
  private count = 0;
  private pos = 0;
  private readonly ratio: number;
  // level is the loudness of the latest block, 0..1.
  level = 0;
  // interrupted says the system suspended the recording at some point (e.g. while the phone
  // was locked), so the memo misses that stretch.
  interrupted = false;
  private closed = false;

  private constructor(
    private ctx: AudioContext,
    private stream: MediaStream,
    private source: MediaStreamAudioSourceNode,
    private node: AudioWorkletNode,
  ) {
    this.ratio = Math.max(1, ctx.sampleRate / SAMPLE_RATE);
    node.port.onmessage = (e: MessageEvent<Float32Array>) => this.take(e.data);
    ctx.addEventListener('statechange', this.noteInterruption);
    this.watch(stream);
  }

  // start asks for the microphone and starts recording.
  static async start(): Promise<WavRecorder> {
    if (!navigator.mediaDevices?.getUserMedia || typeof AudioWorkletNode === 'undefined') throw new RecorderUnsupportedError();
    const stream = await microphone();
    const ctx = new AudioContext();
    try {
      await ctx.audioWorklet.addModule('/recorder-worklet.js');
      const node = new AudioWorkletNode(ctx, 'knowpod-recorder');
      const source = ctx.createMediaStreamSource(stream);
      source.connect(node);
      node.connect(ctx.destination); // silent; keeps the worklet running everywhere
      if (ctx.state === 'suspended') await ctx.resume();
      return new WavRecorder(ctx, stream, source, node);
    } catch (err) {
      stream.getTracks().forEach((t) => t.stop());
      void ctx.close();
      throw err;
    }
  }

  private noteInterruption = () => {
    if (!this.closed && !this.paused && this.ctx.state !== 'running') this.interrupted = true;
  };

  private watch(stream: MediaStream) {
    for (const track of stream.getAudioTracks()) {
      track.addEventListener('mute', this.noteInterruption);
      track.addEventListener('ended', () => {
        if (!this.closed) this.interrupted = true;
      });
    }
  }

  // revive picks the recording up again after the system suspended it: it resumes the audio
  // context and, if the microphone was taken away, asks for it again. Call it when the page
  // is visible again.
  async revive(): Promise<void> {
    if (this.closed) return;
    try {
      if (this.stream.getAudioTracks().every((t) => t.readyState === 'ended')) {
        const stream = await microphone();
        if (this.closed) {
          stream.getTracks().forEach((t) => t.stop());
          return;
        }
        this.source.disconnect();
        this.stream = stream;
        this.source = this.ctx.createMediaStreamSource(stream);
        this.source.connect(this.node);
        this.watch(stream);
      }
      if (this.ctx.state !== 'running') await this.ctx.resume();
    } catch {
      // Tried again on the next return to the page.
    }
  }

  private take(block: Float32Array) {
    let peak = 0;
    for (let i = 0; i < block.length; i++) peak = Math.max(peak, Math.abs(block[i]));
    this.level = peak;
    if (this.paused) return;
    const out = new Int16Array(Math.ceil(block.length / this.ratio) + 1);
    let n = 0;
    for (let i = 0; i < block.length; i++) {
      this.sum += block[i];
      this.count++;
      if (++this.pos >= this.ratio) {
        this.pos -= this.ratio;
        const v = Math.max(-1, Math.min(1, this.sum / this.count));
        out[n++] = v < 0 ? v * 0x8000 : v * 0x7fff;
        this.sum = 0;
        this.count = 0;
      }
    }
    this.chunks.push(out.subarray(0, n));
    this.samples += n;
  }

  // elapsedMs is how long the recording is so far (pauses left out).
  get elapsedMs(): number {
    return (this.samples / SAMPLE_RATE) * 1000;
  }

  get isPaused(): boolean {
    return this.paused;
  }

  pause() {
    this.paused = true;
  }

  resume() {
    this.paused = false;
  }

  private release() {
    this.closed = true;
    this.ctx.removeEventListener('statechange', this.noteInterruption);
    this.node.port.onmessage = null;
    this.node.disconnect();
    this.stream.getTracks().forEach((t) => t.stop());
    void this.ctx.close();
  }

  // stop ends the recording and returns it as a WAV file.
  stop(): Blob {
    this.release();
    return wav(this.chunks, this.samples);
  }

  // cancel ends the recording and drops it.
  cancel() {
    this.release();
    this.chunks = [];
    this.samples = 0;
  }
}

function microphone(): Promise<MediaStream> {
  return navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } });
}

// wav builds a 16-bit mono PCM WAV file of the samples.
function wav(chunks: Int16Array<ArrayBuffer>[], samples: number): Blob {
  const header = new DataView(new ArrayBuffer(44));
  const str = (at: number, s: string) => [...s].forEach((c, i) => header.setUint8(at + i, c.charCodeAt(0)));
  const bytes = samples * 2;
  str(0, 'RIFF');
  header.setUint32(4, 36 + bytes, true);
  str(8, 'WAVE');
  str(12, 'fmt ');
  header.setUint32(16, 16, true); // fmt chunk size
  header.setUint16(20, 1, true); // PCM
  header.setUint16(22, 1, true); // mono
  header.setUint32(24, SAMPLE_RATE, true);
  header.setUint32(28, SAMPLE_RATE * 2, true); // bytes per second
  header.setUint16(32, 2, true); // block align
  header.setUint16(34, 16, true); // bits per sample
  str(36, 'data');
  header.setUint32(40, bytes, true);
  return new Blob([header.buffer, ...chunks], { type: 'audio/wav' });
}
