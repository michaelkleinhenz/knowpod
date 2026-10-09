import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useRecorder } from '../context/Recorder';
import type { SystemAudioSink } from '../lib/desktop';
import { listOutputs, outputInput, SystemAudioError, systemAudioAvailable, systemAudioErrorText } from '../lib/systemAudio';
import { InputOpener, microphoneInput, RecorderInput, WORKLET } from '../lib/wavRecorder';

// meetingRecorderAvailable says whether the meeting recorder's button is shown: in the desktop
// app on Linux, which can record what the computer plays.
export const meetingRecorderAvailable = () => systemAudioAvailable();

// The devices picked last, kept in this browser ('' is none).
const micStorageKey = 'knowpod.meetingMicrophone';
const speakerStorageKey = 'knowpod.meetingSpeaker';

function stored(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function store(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // not kept: fine
  }
}

// Below this peak level an input counts as silent.
const silence = 0.01;

// MeetingRecorderDialog records a video meeting held on this computer (e.g. Google Meet in the
// browser): the microphone (you) and what the computer plays (the others), each in a channel
// of its own. It picks the two devices and shows their loudness, so you see something comes
// in before you start; the recording itself runs in the recorder (context/Recorder.tsx) like
// a voice memo.
export function MeetingRecorderDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const recorder = useRecorder();
  const [mics, setMics] = useState<MediaDeviceInfo[] | null>(null);
  const [speakers, setSpeakers] = useState<SystemAudioSink[] | null>(null);
  const [mic, setMic] = useState<string>('');
  const [speaker, setSpeaker] = useState<string>('');
  const [micError, setMicError] = useState<string | null>(null);
  const [speakerError, setSpeakerError] = useState<string | null>(null);
  // ctx is the audio context of the meters; null until its worklets are loaded.
  const [ctx, setCtx] = useState<AudioContext | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  useEffect(() => {
    const context = new AudioContext();
    let closed = false;
    context.audioWorklet.addModule(WORKLET).then(
      () => !closed && setCtx(context),
      () => undefined,
    );
    return () => {
      closed = true;
      void context.close();
    };
  }, []);

  // The microphones: their names show only once the microphone was allowed, so ask first.
  const loadMics = useCallback(async () => {
    try {
      const probe = await navigator.mediaDevices.getUserMedia({ audio: true });
      probe.getTracks().forEach((track) => track.stop());
    } catch {
      // listed without names then
    }
    try {
      const devices = (await navigator.mediaDevices.enumerateDevices()).filter((d) => d.kind === 'audioinput');
      setMics(devices);
      setMic((current) => {
        if (current && devices.some((d) => d.deviceId === current)) return current;
        const kept = stored(micStorageKey);
        if (kept === '' || (kept && devices.some((d) => d.deviceId === kept))) return kept;
        return devices[0]?.deviceId ?? '';
      });
    } catch (err) {
      setMics([]);
      setMicError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  const loadSpeakers = useCallback(async () => {
    try {
      const sinks = await listOutputs();
      setSpeakers(sinks);
      setSpeakerError(null);
      setSpeaker((current) => {
        if (current && sinks.some((s) => s.name === current)) return current;
        const kept = stored(speakerStorageKey);
        if (kept === '' || (kept && sinks.some((s) => s.name === kept))) return kept;
        return (sinks.find((s) => s.default) ?? sinks[0])?.name ?? '';
      });
    } catch (err) {
      setSpeakers([]);
      setSpeakerError(err instanceof SystemAudioError ? systemAudioErrorText(err, t) : String(err));
    }
  }, [t]);

  useEffect(() => {
    void loadMics();
    void loadSpeakers();
    const onChange = () => {
      void loadMics();
      void loadSpeakers();
    };
    navigator.mediaDevices.addEventListener('devicechange', onChange);
    return () => navigator.mediaDevices.removeEventListener('devicechange', onChange);
  }, [loadMics, loadSpeakers]);

  const pickMic = (id: string) => {
    setMic(id);
    store(micStorageKey, id);
  };
  const pickSpeaker = (name: string) => {
    setSpeaker(name);
    store(speakerStorageKey, name);
  };

  const inputs: InputOpener[] = [];
  if (mic) inputs.push(microphoneInput(mic));
  if (speaker) inputs.push(outputInput(speaker));
  const busy = recorder.active;

  function start() {
    if (inputs.length === 0 || busy) return;
    // The meters let go of the devices first (the dialog's audio context closes with it).
    onClose();
    recorder.startMeeting(inputs);
  }

  const heading = t('meeting.title');
  return (
    <div className="new-item-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="new-item-dialog meeting-dialog" role="dialog" aria-modal="true" aria-label={heading}>
        <h2>{heading}</h2>
        <p className="muted">{t('meeting.intro')}</p>

        <label className="meeting-device">
          <span>{t('meeting.microphone')}</span>
          <select value={mic} onChange={(e) => pickMic(e.target.value)} disabled={!mics}>
            {!mics && <option value={mic}>{t('common.loading')}</option>}
            {mics?.map((d, i) => (
              <option key={d.deviceId || i} value={d.deviceId}>
                {d.label || t('meeting.microphoneN', { n: i + 1 })}
              </option>
            ))}
            <option value="">{t('meeting.none')}</option>
          </select>
        </label>
        <Meter ctx={ctx} open={mic ? microphoneInput(mic) : null} openKey={`mic:${mic}`} onError={setMicError} />
        {micError && <p className="error">{micError}</p>}

        <label className="meeting-device">
          <span>{t('meeting.speaker')}</span>
          <select value={speaker} onChange={(e) => pickSpeaker(e.target.value)} disabled={!speakers}>
            {!speakers && <option value={speaker}>{t('common.loading')}</option>}
            {speakers?.map((s) => (
              <option key={s.name} value={s.name}>
                {s.default ? t('meeting.defaultOutput', { name: s.description }) : s.description}
              </option>
            ))}
            <option value="">{t('meeting.none')}</option>
          </select>
        </label>
        <Meter
          ctx={ctx}
          open={speaker ? outputInput(speaker) : null}
          openKey={`speaker:${speaker}`}
          onError={(e) => setSpeakerError(e)}
          errorText={(err) => (err instanceof SystemAudioError ? systemAudioErrorText(err, t) : null)}
        />
        {speakerError && <p className="error">{speakerError}</p>}

        <p className="field-hint">{t('meeting.hint')}</p>
        <p className="meeting-consent">{t('meeting.consent')}</p>
        {busy && <p className="field-hint">{t('meeting.busy')}</p>}

        <div className="new-item-actions">
          <button type="button" className="pill-button" onClick={onClose}>
            {t('nav.cancel')}
          </button>
          <button type="button" className="pill-button primary" onClick={start} disabled={inputs.length === 0 || busy}>
            {t('meeting.start')}
          </button>
        </div>
      </div>
    </div>
  );
}

// Meter shows how loud an input is right now; openKey says when open is another input.
function Meter({
  ctx,
  open,
  openKey,
  onError,
  errorText,
}: {
  ctx: AudioContext | null;
  open: InputOpener | null;
  openKey: string;
  onError: (message: string | null) => void;
  errorText?: (err: unknown) => string | null;
}) {
  const { t } = useTranslation();
  const bar = useRef<HTMLSpanElement>(null);
  const [heard, setHeard] = useState<boolean | null>(null);
  // open changes with every render; only openKey says it's another input.
  const openRef = useRef(open);
  openRef.current = open;
  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;
  const errorTextRef = useRef(errorText);
  errorTextRef.current = errorText;

  useEffect(() => {
    setHeard(null);
    if (bar.current) bar.current.style.transform = 'scaleX(0)';
    const opener = openRef.current;
    if (!ctx || !opener) return;
    let stopped = false;
    let input: RecorderInput | null = null;
    let frame = 0;
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 1024;
    // Not to be heard: analysers only run when connected on to the speakers.
    const mute = ctx.createGain();
    mute.gain.value = 0;
    analyser.connect(mute);
    mute.connect(ctx.destination);
    const samples = new Float32Array(analyser.fftSize);
    let peakHold = 0;
    let lastHeard = 0;
    const draw = () => {
      analyser.getFloatTimeDomainData(samples);
      let peak = 0;
      for (let i = 0; i < samples.length; i++) peak = Math.max(peak, Math.abs(samples[i]));
      // Falls back slowly, so short sounds stay visible.
      peakHold = Math.max(peak, peakHold * 0.92);
      if (bar.current) bar.current.style.transform = `scaleX(${Math.min(1, Math.sqrt(peakHold))})`;
      const now = performance.now();
      if (peak > silence) lastHeard = now;
      const isHeard = now - lastHeard < 1500;
      setHeard((h) => (h === isHeard ? h : isHeard));
      frame = requestAnimationFrame(draw);
    };
    onErrorRef.current(null);
    if (ctx.state === 'suspended') void ctx.resume();
    opener(ctx).then(
      (opened) => {
        if (stopped) {
          opened.close();
          return;
        }
        input = opened;
        opened.node.connect(analyser);
        lastHeard = -Infinity;
        frame = requestAnimationFrame(draw);
      },
      (err) => {
        if (stopped) return;
        const text = errorTextRef.current?.(err);
        onErrorRef.current(
          text ??
            (err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'SecurityError')
              ? t('recorder.denied')
              : err instanceof Error
                ? err.message
                : String(err)),
        );
      },
    );
    return () => {
      stopped = true;
      cancelAnimationFrame(frame);
      input?.close();
      analyser.disconnect();
      mute.disconnect();
    };
  }, [ctx, openKey, t]);

  if (!open) return null;
  return (
    <div className="meeting-meter">
      <span className="meeting-level" aria-hidden="true">
        <span ref={bar} />
      </span>
      <span className={`meeting-heard${heard ? ' on' : ''}`} role="status">
        {heard === null ? t('meeting.listening') : heard ? t('meeting.heard') : t('meeting.silent')}
      </span>
    </div>
  );
}
