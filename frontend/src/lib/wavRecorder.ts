// WavRecorder records the microphone as 16 kHz mono 16-bit WAV, the format the server
// archives as FLAC and transcribes (speech needs no more). A meeting is recorded in stereo:
// one channel per input, the microphone left and what the computer plays right (see
// MeetingRecorder.tsx); the server mixes them down for transcribing. The audio is taken with an audio
// worklet (public/recorder-worklet.js) and averaged down to 16 kHz as it comes in, so a long
// memo takes about 2 MB of memory per minute.
//
// Phones may suspend the recording when the screen is locked: Android keeps capturing while
// the microphone is in use, but iOS suspends the page (the audio context reports
// "interrupted" and the microphone may be muted or ended). The recorder notes such an
// interruption and revive() picks the recording up again when the page is back.

export const SAMPLE_RATE = 16_000;

export class RecorderUnsupportedError extends Error {}

// RecorderInput is an audio source opened in the recorder's audio context: one channel of the
// recording. ended says the system took it away (revive opens it again); watch tells when it
// is muted or ends.
export interface RecorderInput {
  node: AudioNode;
  ended(): boolean;
  watch(muted: () => void, ended: () => void): void;
  close(): void;
}

// InputOpener opens an input in the recorder's audio context (the worklet module is loaded).
export type InputOpener = (ctx: AudioContext) => Promise<RecorderInput>;

// microphoneInput is a microphone: the system's default one, or the one with deviceId.
export function microphoneInput(deviceId?: string): InputOpener {
  return async (ctx) => streamInput(ctx, await microphone(deviceId));
}

// streamInput makes an input of a media stream.
export function streamInput(ctx: AudioContext, stream: MediaStream): RecorderInput {
  const node = ctx.createMediaStreamSource(stream);
  return {
    node,
    ended: () => stream.getAudioTracks().every((t) => t.readyState === 'ended'),
    watch: (muted, ended) => {
      for (const track of stream.getAudioTracks()) {
        track.addEventListener('mute', muted);
        track.addEventListener('ended', ended);
      }
    },
    close: () => {
      node.disconnect();
      stream.getTracks().forEach((t) => t.stop());
    },
  };
}

// WORKLET is the recorder's audio worklets (recorder-worklet.js).
export const WORKLET = '/recorder-worklet.js';

export class WavRecorder {
  private chunks: Int16Array<ArrayBuffer>[] = [];
  private frames = 0;
  private paused = false;
  // Averaging down: the input samples summed (per channel) for the next output sample, and how far into it.
  private sum: number[];
  private count = 0;
  private pos = 0;
  private readonly ratio: number;
  readonly channels: number;
  // levels is the loudness of the latest block of each channel, 0..1; level the loudest.
  levels: number[];
  level = 0;
  // interrupted says the system suspended the recording at some point (e.g. while the phone
  // was locked), so the memo misses that stretch.
  interrupted = false;
  private closed = false;

  private constructor(
    private ctx: AudioContext,
    private inputs: RecorderInput[],
    private openers: InputOpener[],
    private target: AudioNode,
    private node: AudioWorkletNode,
  ) {
    this.channels = inputs.length;
    this.sum = new Array<number>(this.channels).fill(0);
    this.levels = new Array<number>(this.channels).fill(0);
    this.ratio = Math.max(1, ctx.sampleRate / SAMPLE_RATE);
    node.port.onmessage = (e: MessageEvent<Float32Array[]>) => this.take(e.data);
    ctx.addEventListener('statechange', this.noteInterruption);
    inputs.forEach((input) => this.watch(input));
  }

  // start starts recording: the microphone, or the inputs given (one channel each, at most 2).
  static async start(openers: InputOpener[] = [microphoneInput()]): Promise<WavRecorder> {
    if (!navigator.mediaDevices?.getUserMedia || typeof AudioWorkletNode === 'undefined') throw new RecorderUnsupportedError();
    const ctx = new AudioContext();
    const inputs: RecorderInput[] = [];
    try {
      await ctx.audioWorklet.addModule(WORKLET);
      for (const open of openers) inputs.push(await open(ctx));
      const channels = inputs.length;
      const node = new AudioWorkletNode(ctx, 'knowpod-recorder', { processorOptions: { channels } });
      // Stereo: each input mixed down into its own channel.
      const target = channels === 1 ? node : ctx.createChannelMerger(channels);
      inputs.forEach((input, i) => input.node.connect(target, 0, channels === 1 ? 0 : i));
      if (target !== node) target.connect(node);
      node.connect(ctx.destination); // silent; keeps the worklet running everywhere
      if (ctx.state === 'suspended') await ctx.resume();
      return new WavRecorder(ctx, inputs, openers, target, node);
    } catch (err) {
      inputs.forEach((input) => input.close());
      void ctx.close();
      throw err;
    }
  }

  private noteInterruption = () => {
    if (!this.closed && !this.paused && this.ctx.state !== 'running') this.interrupted = true;
  };

  private watch(input: RecorderInput) {
    input.watch(this.noteInterruption, () => {
      if (!this.closed) this.interrupted = true;
    });
  }

  // revive picks the recording up again after the system suspended it: it resumes the audio
  // context and opens inputs that were taken away again. Call it when the page is visible
  // again.
  async revive(): Promise<void> {
    if (this.closed) return;
    try {
      for (let i = 0; i < this.inputs.length; i++) {
        if (!this.inputs[i].ended()) continue;
        const input = await this.openers[i](this.ctx);
        if (this.closed) {
          input.close();
          return;
        }
        this.inputs[i].close();
        this.inputs[i] = input;
        input.node.connect(this.target, 0, this.channels === 1 ? 0 : i);
        this.watch(input);
      }
      if (this.ctx.state !== 'running') await this.ctx.resume();
    } catch {
      // Tried again on the next return to the page.
    }
  }

  private take(blocks: Float32Array[]) {
    const nch = this.channels;
    if (!Array.isArray(blocks) || blocks.length < nch) return;
    for (let c = 0; c < nch; c++) {
      let peak = 0;
      for (let i = 0; i < blocks[c].length; i++) peak = Math.max(peak, Math.abs(blocks[c][i]));
      this.levels[c] = peak;
    }
    this.level = Math.max(...this.levels);
    if (this.paused) return;
    const length = blocks[0].length;
    const out = new Int16Array((Math.ceil(length / this.ratio) + 1) * nch);
    let n = 0;
    for (let i = 0; i < length; i++) {
      for (let c = 0; c < nch; c++) this.sum[c] += blocks[c][i];
      this.count++;
      if (++this.pos >= this.ratio) {
        this.pos -= this.ratio;
        for (let c = 0; c < nch; c++) {
          const v = Math.max(-1, Math.min(1, this.sum[c] / this.count));
          out[n++] = v < 0 ? v * 0x8000 : v * 0x7fff;
          this.sum[c] = 0;
        }
        this.count = 0;
      }
    }
    this.chunks.push(out.subarray(0, n));
    this.frames += n / nch;
  }

  // elapsedMs is how long the recording is so far (pauses left out).
  get elapsedMs(): number {
    return (this.frames / SAMPLE_RATE) * 1000;
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
    this.inputs.forEach((input) => input.close());
    void this.ctx.close();
  }

  // stop ends the recording and returns it as a WAV file.
  stop(): Blob {
    this.release();
    return wav(this.chunks, this.frames, this.channels);
  }

  // cancel ends the recording and drops it.
  cancel() {
    this.release();
    this.chunks = [];
    this.frames = 0;
  }
}

// microphone asks for the microphone (the one with deviceId, if given), cleaned up for speech.
export function microphone(deviceId?: string): Promise<MediaStream> {
  return navigator.mediaDevices.getUserMedia({
    audio: { ...(deviceId ? { deviceId: { exact: deviceId } } : {}), echoCancellation: true, noiseSuppression: true, autoGainControl: true },
  });
}

// wav builds a 16-bit PCM WAV file of the (interleaved) samples.
function wav(chunks: Int16Array<ArrayBuffer>[], frames: number, channels: number): Blob {
  const header = new DataView(new ArrayBuffer(44));
  const str = (at: number, s: string) => [...s].forEach((c, i) => header.setUint8(at + i, c.charCodeAt(0)));
  const bytes = frames * channels * 2;
  str(0, 'RIFF');
  header.setUint32(4, 36 + bytes, true);
  str(8, 'WAVE');
  str(12, 'fmt ');
  header.setUint32(16, 16, true); // fmt chunk size
  header.setUint16(20, 1, true); // PCM
  header.setUint16(22, channels, true);
  header.setUint32(24, SAMPLE_RATE, true);
  header.setUint32(28, SAMPLE_RATE * channels * 2, true); // bytes per second
  header.setUint16(32, channels * 2, true); // block align
  header.setUint16(34, 16, true); // bits per sample
  str(36, 'data');
  header.setUint32(40, bytes, true);
  return new Blob([header.buffer, ...chunks], { type: 'audio/wav' });
}
