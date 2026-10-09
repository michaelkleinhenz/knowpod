// Recording what the computer plays, in the desktop app on Linux: the app records an output's
// monitor (desktop/src/system-audio.js) and streams it here, where it is played into the
// recorder's audio graph by the knowpod-pcm-source worklet (public/recorder-worklet.js), so
// it can be recorded beside the microphone (see MeetingRecorder.tsx).
import type { TFunction } from 'i18next';
import { systemAudioBridge } from './desktop';
import type { InputOpener, RecorderInput } from './wavRecorder';

// SystemAudioError says why what the computer plays can't be recorded: unsupported,
// missing-tools, no-server or failed (see systemAudioErrorText).
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
      return t(`meeting.errors.${err.code}`);
    default:
      return t('meeting.errors.failed', { detail: err.detail || err.code });
  }
}

// systemAudioAvailable says whether this app can record what the computer plays.
export const systemAudioAvailable = () => !!systemAudioBridge();

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
    const node = new AudioWorkletNode(ctx, 'knowpod-pcm-source', { numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [1] });
    const r = await b.call({ action: 'start', sink, rate: ctx.sampleRate });
    if (!r.ok || r.id === undefined) {
      node.disconnect();
      throw new SystemAudioError(r.error || 'failed', r.message);
    }
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
