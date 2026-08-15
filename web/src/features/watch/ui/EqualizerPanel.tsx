import { useEffect, useState } from 'react'
import clsx from 'clsx'

import {
  BANDS,
  MAX_BAND_DB,
  MIN_PREAMP_DB,
  MAX_PREAMP_DB,
  PRESETS,
  identifyPreset,
  settingsForPreset,
  type EqSettings,
  type PresetName,
} from '@/features/watch/application/eq-presets'
import {
  DEFAULT_WET,
  REVERB_PRESETS,
  type ReverbPresetName,
  type ReverbSettings,
} from '@/features/watch/application/reverb-presets'
import type { AudioSettings } from '@/features/watch/application/audio-prefs'

/**
 * The equaliser and the room, as they appear behind the player's Audio button.
 *
 * A group of `<li>`s rather than a component of its own, because the menu is a
 * list. It renders in the player rather than on a settings screen for one
 * reason: this is adjusted by ear against something playing, and a curve chosen
 * in silence on another page is a curve chosen blind.
 */
export function EqualizerSetting({
  audio,
  onChange,
  element,
}: {
  audio: AudioSettings
  onChange: (next: AudioSettings) => void
  /** The video in front, watched only to know whether it went full screen. */
  element: HTMLVideoElement | null
}) {
  const bypassed = useNativeFullscreen(element)
  const settings = audio.eq
  const reverb = audio.reverb
  const setEq = (next: EqSettings) => onChange({ ...audio, eq: next })
  const setReverb = (next: ReverbSettings) => onChange({ ...audio, reverb: next })

  const setGain = (index: number, db: number) => {
    const gains = settings.gains.map((g, i) => (i === index ? db : g))
    // Enabling on the first drag, rather than making the viewer find the switch
    // first: moving a slider is unambiguously a request to hear the difference,
    // and a control that does nothing until a second control is found is the
    // dead button the charter forbids.
    const next: EqSettings = { ...settings, gains, enabled: true, preset: 'custom' }
    setEq({ ...next, preset: identifyPreset(next) })
  }

  const setPreamp = (db: number) => {
    const next: EqSettings = { ...settings, preamp: db, enabled: true, preset: 'custom' }
    setEq({ ...next, preset: identifyPreset(next) })
  }

  const pickPreset = (name: PresetName) => {
    if (name === 'custom') return
    setEq(settingsForPreset(name))
  }

  /**
   * Choosing a room selects it. Switching the reverb off is the switch's job.
   *
   * Pressing the lit room used to turn the whole thing off, which was the only
   * way to reach silence when there was no switch. Now that Environment has one
   * — the same row the equaliser has, because they are the same kind of thing —
   * that would be a second, hidden control for a setting already on screen, and
   * the two would disagree about what pressing a lit room means.
   */
  const pickRoom = (name: ReverbPresetName) => {
    setReverb({ enabled: true, preset: name, wet: reverb.wet || DEFAULT_WET })
  }

  return (
    <>
      <SettingRowLike
        label="Equalizer"
        on={settings.enabled}
        onToggle={() => setEq({ ...settings, enabled: !settings.enabled })}
      />

      {settings.enabled && (
        <li className="px-4 pb-3">
          {/*
            Named for what is happening rather than for the browser's rule.
            On iOS the native full-screen player takes the video's audio out of
            Web Audio entirely, so the filters stop applying — silently, with no
            event and nothing on screen to explain it. Shown only while it is
            actually true: a permanent warning is read once and then stops being
            read at all, and this is a question that only occurs to somebody at
            the moment the sound stops changing.
          */}
          {bypassed && (
            <p className="pb-2 text-xs text-brand" role="status">
              EQ off in fullscreen
            </p>
          )}

          <div
            role="radiogroup"
            aria-label="Equalizer preset"
            className="flex flex-wrap gap-1 pb-3"
          >
            {PRESETS.map((p) => {
              const on = settings.preset === p.name
              return (
                <button
                  key={p.name}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  onClick={() => pickPreset(p.name)}
                  className={clsx(
                    'min-h-11 rounded-md px-3 text-xs font-medium transition-colors duration-150 ease-out',
                    on
                      ? 'bg-invert-bg text-invert-text'
                      : 'bg-white/10 text-text-2 hover:bg-white/20 hover:text-text',
                  )}
                >
                  {p.label}
                </button>
              )
            })}
            {/* Shown as a state, never as a choice: "Custom" is where the
                sliders put you, and offering it as a button would raise the
                question of what pressing it restores. */}
            {settings.preset === 'custom' && (
              <span className="rounded-md bg-invert-bg px-2 py-1.5 text-xs font-medium text-invert-text">
                Custom
              </span>
            )}
          </div>

          {/*
            Vertical sliders, because a graphic equaliser is read as a shape.
            Ten horizontal rows would be the same numbers and none of the
            information — the whole point is seeing the curve at a glance.

            `writing-mode` rather than a rotation: a rotated input keeps its
            original hit box, so the pointer would have to be dragged sideways to
            move a slider that appears to run up and down.
          */}
          <div className="flex items-end justify-between gap-1">
            {BANDS.map((band, i) => (
              <div key={band.frequency} className="flex flex-1 flex-col items-center gap-1">
                <span className="text-[10px] tabular-nums text-text-2">
                  {formatDb(settings.gains[i])}
                </span>
                <BandSlider
                  label={band.label}
                  value={settings.gains[i]}
                  onChange={(db) => setGain(i, db)}
                />
                <span className="text-[10px] text-text-2">{band.label}</span>
              </div>
            ))}
          </div>

          <div className="flex items-center gap-2 pt-3">
            <span className="w-14 shrink-0 text-xs text-text-2">Preamp</span>
            <input
              type="range"
              aria-label="Preamp"
              min={MIN_PREAMP_DB}
              max={MAX_PREAMP_DB}
              step={1}
              value={settings.preamp}
              onChange={(e) => setPreamp(Number(e.target.value))}
              // A horizontal range is only as tall as it is drawn, and what is
              // drawn is a few pixels of track. The element is given the full
              // 44 to be grabbed by; the track inside it does not change.
              className="h-11 min-w-0 flex-1 accent-brand"
            />
            <span className="w-10 shrink-0 text-right text-[10px] tabular-nums text-text-2">
              {formatDb(settings.preamp)}
            </span>
          </div>
          {/*
            Why it only cuts. Boosting bands adds gain to material already
            mastered near full scale, and the sum clips — heard as crackle rather
            than as loudness. The preamp is the room those boosts are paid for
            out of, so a positive setting would defeat the one thing it is for.
          */}
          <p className="pt-1 text-[10px] text-text-2">
            Lower the preamp if boosted bands distort.
          </p>
        </li>
      )}

      {/*
        The room. Named "Environment" after the control it imitates, and offering
        four spaces rather than the dozen macOS has: these are synthesised rather
        than recorded, and two labels over one sound would be a control that
        cannot do what its name says.
      */}
      <SettingRowLike
        label="Environment"
        on={reverb.enabled}
        onToggle={() => setReverb({ ...reverb, enabled: !reverb.enabled })}
      />

      {reverb.enabled && (
      <li className="px-4 pb-3">
        <div role="radiogroup" aria-label="Environment" className="flex flex-wrap gap-1">
          {REVERB_PRESETS.map((p) => {
            const on = reverb.preset === p.name
            return (
              <button
                key={p.name}
                type="button"
                role="radio"
                aria-checked={on}
                onClick={() => pickRoom(p.name)}
                className={clsx(
                  'min-h-11 rounded-md px-3 text-xs font-medium transition-colors duration-150 ease-out',
                  on
                    ? 'bg-invert-bg text-invert-text'
                    : 'bg-white/10 text-text-2 hover:bg-white/20 hover:text-text',
                )}
              >
                {p.label}
              </button>
            )
          })}
        </div>

        <div className="flex items-center gap-2 pt-2">
          <span className="w-14 shrink-0 text-xs text-text-2">Dry/Wet</span>
          <input
            type="range"
            aria-label="Dry wet mix"
            min={0}
            max={100}
            step={1}
            value={Math.round(reverb.wet * 100)}
            onChange={(e) => setReverb({ ...reverb, wet: Number(e.target.value) / 100 })}
            className="h-11 min-w-0 flex-1 accent-brand"
          />
          <span className="w-10 shrink-0 text-right text-[10px] tabular-nums text-text-2">
            {Math.round(reverb.wet * 100)}%
          </span>
        </div>
        {bypassed && (
          <p className="pt-1 text-[10px] text-brand" role="status">
            Also off in fullscreen
          </p>
        )}
      </li>
      )}
    </>
  )
}

function formatDb(db: number): string {
  return `${db > 0 ? '+' : ''}${db}`
}

/**
 * One band of the equaliser: a thin track, and a wide invisible input over it.
 *
 * The native control was doing both jobs and could only do one of them well. A
 * range input's hit box is the element itself and nothing around it, so making
 * it thin enough to look like a fader left 4px of grabbable width with dead
 * space either side, and making it wide enough to grab painted the *track* 31px
 * across — a grey slab, ten of them in a row.
 *
 * So the input keeps the whole column, and gives up drawing. It is still the
 * real control: it carries the value, the keyboard (arrows, Home/End), the
 * label, and the focus ring, all of which would have to be rebuilt by hand
 * otherwise, badly. Only its appearance is ours.
 *
 * That also softens the one risk this panel has. The vertical axis comes from
 * `writing-mode`, which older Safari ignores — and where it is ignored, the
 * fader still looks exactly right and only the drag runs along the wrong axis.
 * Before, the whole row would have collapsed into ten horizontal sliders.
 *
 * The fill grows from the centre rather than from the bottom, because zero is
 * in the middle of ±12dB and the thing worth seeing at a glance is which way a
 * band was pushed and how far. Filling from the bottom would draw a flat
 * equaliser as ten half-full bars — a shape, and the wrong one.
 */
function BandSlider({
  label,
  value,
  onChange,
}: {
  label: string
  value: number
  onChange: (db: number) => void
}) {
  // 0 at the top of the box, 1 at the bottom — the direction the fader reads.
  const position = (MAX_BAND_DB - value) / (MAX_BAND_DB * 2)
  const centre = 0.5
  const top = Math.min(position, centre)
  const height = Math.abs(position - centre)

  // The focus ring is drawn on the box below, not on the input.
  //
  // Hiding the input hid its outline with it, and a keyboard user tabbing
  // through ten identical faders with nothing to show which one has the keys is
  // worse than the slab this replaced. `has-[:focus-visible]` puts it back on
  // the box the viewer actually sees, and only for the keyboard — a mouse press
  // does not light it up.
  return (
    <div className="relative h-32 w-full rounded has-[:focus-visible]:outline has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-brand">
      {/* The track, and the only part that is 4px wide. */}
      <div className="absolute left-1/2 top-0 h-full w-1 -translate-x-1/2 rounded-full bg-white/15" />

      {/* Zero, marked. Without it the centre is a place you have to count to. */}
      <div className="absolute left-1/2 top-1/2 h-px w-2.5 -translate-x-1/2 -translate-y-1/2 bg-white/30" />

      {value !== 0 && (
        <div
          className="absolute left-1/2 w-1 -translate-x-1/2 rounded-full bg-brand"
          style={{ top: `${top * 100}%`, height: `${height * 100}%` }}
        />
      )}

      <div
        className="absolute left-1/2 h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full bg-white shadow-md ring-1 ring-black/20"
        style={{ top: `${position * 100}%` }}
      />

      <input
        type="range"
        aria-label={`${label} hertz`}
        min={-MAX_BAND_DB}
        max={MAX_BAND_DB}
        step={1}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        // Back to flat, without hunting for the centre by hand.
        onDoubleClick={() => onChange(0)}
        className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
        style={{ writingMode: 'vertical-lr', direction: 'rtl' }}
      />
    </div>
  )
}

/**
 * Whether this element is in the system's own full-screen player.
 *
 * `webkitPresentationMode` is a non-standard Safari property and is the only way
 * to tell iOS's native player apart from the page being made full screen — which
 * matters because they behave completely differently here: the page going full
 * screen keeps the audio graph, and the native player does not.
 *
 * Polled on the presentation-mode event where Safari provides one, and on the
 * ordinary fullscreen change elsewhere. Browsers without either simply never
 * report a bypass, which is correct for them: they have no native player to be
 * bypassed by.
 */
function useNativeFullscreen(element: HTMLVideoElement | null): boolean {
  const [bypassed, setBypassed] = useState(false)

  useEffect(() => {
    if (!element) {
      setBypassed(false)
      return
    }
    const read = () => {
      const mode = (element as HTMLVideoElement & { webkitPresentationMode?: string })
        .webkitPresentationMode
      setBypassed(mode === 'fullscreen')
    }
    read()
    element.addEventListener('webkitpresentationmodechanged', read)
    document.addEventListener('fullscreenchange', read)
    return () => {
      element.removeEventListener('webkitpresentationmodechanged', read)
      document.removeEventListener('fullscreenchange', read)
    }
  }, [element])

  return bypassed
}

/**
 * The same on/off line the rest of the menu uses.
 *
 * Duplicated from `Player.tsx` rather than exported from it, for now: that file
 * exports one component and pulling a private one out of it to reach this would
 * be a wider change than the equaliser has any business making. If a third
 * caller appears, it moves to a shared module.
 */
function SettingRowLike({
  label,
  on,
  onToggle,
}: {
  label: string
  on: boolean
  onToggle: () => void
}) {
  return (
    <li>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        onClick={onToggle}
        className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <span>{label}</span>
        <span
          className={clsx(
            'flex h-4 w-8 shrink-0 items-center rounded-full px-0.5 transition-colors duration-150 ease-out',
            on ? 'bg-brand' : 'bg-white/25',
          )}
        >
          <span
            className={clsx(
              'h-3 w-3 rounded-full bg-white transition-transform duration-150 ease-out',
              on && 'translate-x-4',
            )}
          />
        </span>
      </button>
    </li>
  )
}
