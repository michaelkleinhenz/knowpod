// Recording what the computer plays, in the desktop app, so it can be recorded beside the
// microphone (see MeetingRecorder.tsx). On Linux the app records an output's monitor
// (desktop/src/system-audio.js) and streams it here, where it is played into the recorder's
// audio graph by the knowpod-pcm-source worklet (public/recorder-worklet.js). On Windows and
// macOS the app lets this page capture it itself, as the loopback audio of getDisplayMedia.
import type { TFunction } from 'i18next';
import { systemAudioBridge } from './desktop';
import { type InputOpener, type RecorderInput, streamInput } from './wavRecorder';

// SystemAudioError says why what the computer plays can't be recorded: unsupported,
// missing-tools, no-server, denied or failed (see systemAudioErrorText).
export class SystemAudioError extends Error {
  constructor(
    readonly code: string,
    readonly detail = '',
  ) {
    super(detail || code);
  }
}

export function systemAudioErrorText(err: SystemAudioError, t: TFunction): string {
  switch (err.code) {
    case 'unsupported':
    case 'missing-tools':
    case 'no-server':
    case 'denied':
      return t(`meeting.errors.${err.code}`);
    default:
      return t('meeting.errors.failed', { detail: err.detail || err.code });
  }
}

// systemAudioAvailable says whether this app can record what the computer plays.
export const systemAudioAvailable = () => !!systemAudioBridge();

// LOOPBACK is the one output where the app can only record everything the computer plays
// (Windows, macOS); it has no description of its own.
export const LOOPBACK = 'loopback';

// listOutputs returns the computer's outputs (speakers, headsets), the default one marked.
export async function listOutputs() {
  const b = systemAudioBridge();
  if (!b) throw new SystemAudioError('unsupported');
  const r = await b.call({ action: 'list' });
  if (!r.ok) throw new SystemAudioError(r.error || 'failed', r.message);
  return r.sinks ?? [];
}

// The captures streaming now, by id; one listener serves them all.
const captures = new Map<number, { data: (samples: Float32Array) => void; end: () => void }>();
let listening = false;

function listen() {
  const b = systemAudioBridge();
  if (!b || listening) return;
  listening = true;
  b.listen(
    (id, chunk) => {
      const capture = captures.get(id);
      // A copy: the chunk's bytes need not be aligned for floats.
      if (capture && chunk.byteLength >= 4) capture.data(new Float32Array(chunk.slice(0, chunk.byteLength - (chunk.byteLength % 4)).buffer));
    },
    (id) => captures.get(id)?.end(),
  );
}

// outputInput is what the output named sink plays, as a recorder input. The audio context
// must have the recorder's worklets loaded.
export function outputInput(sink: string): InputOpener {
  return async (ctx): Promise<RecorderInput> => {
    const b = systemAudioBridge();
    if (!b) throw new SystemAudioError('unsupported');
    listen();
    const r = await b.call({ action: 'start', sink, rate: ctx.sampleRate });
    if (r.ok && r.loopback) return streamInput(ctx, await loopbackStream());
    if (!r.ok || r.id === undefined) throw new SystemAudioError(r.error || 'failed', r.message);
    const node = new AudioWorkletNode(ctx, 'knowpod-pcm-source', { numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [1] });
    const id = r.id;
    let ended = false;
    const onEnded: (() => void)[] = [];
    captures.set(id, {
      data: (samples) => node.port.postMessage(samples, [samples.buffer]),
      end: () => {
        ended = true;
        captures.delete(id);
        onEnded.forEach((f) => f());
      },
    });
    return {
      node,
      ended: () => ended,
      watch: (_muted, end) => onEnded.push(end),
      close: () => {
        captures.delete(id);
        void b.call({ action: 'stop', id }).catch(() => undefined);
        node.disconnect();
        node.port.close();
      },
    };
  };
}

// loopbackStream captures what the computer plays (Windows, macOS): the app grants this page's
// next screen capture the loopback audio, with the page itself as the video, which isn't needed.
async function loopbackStream(): Promise<MediaStream> {
  let stream: MediaStream;
  try {
    stream = await navigator.mediaDevices.getDisplayMedia({
      video: true,
      audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false },
    });
  } catch (err) {
    const denied = err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'SecurityError');
    throw new SystemAudioError(denied ? 'denied' : 'failed', err instanceof Error ? err.message : String(err));
  }
  for (const track of stream.getVideoTracks()) {
    track.stop();
    stream.removeTrack(track);
  }
  if (stream.getAudioTracks().length === 0) {
    stream.getTracks().forEach((track) => track.stop());
    throw new SystemAudioError('failed', 'no audio');
  }
  return stream;
}
