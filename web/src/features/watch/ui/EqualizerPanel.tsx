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

/**
 * The equaliser, as it appears inside the player's gear menu.
 *
 * A group of `<li>`s rather than a component of its own, because the menu is a
 * list and this has to sit in it beside subtitles and quality. It renders in the
 * player rather than on a settings screen for one reason: an equaliser is
 * adjusted by ear against something playing, and a curve chosen in silence on
 * another page is a curve chosen blind.
 */
export function EqualizerSetting({
  settings,
  onChange,
  element,
  tall,
}: {
  settings: EqSettings
  onChange: (next: EqSettings) => void
  /** The video in front, watched only to know whether it went full screen. */
  element: HTMLVideoElement | null
  /** Touch targets need 44px; a mouse is happy with less. */
  tall?: boolean
}) {
  const bypassed = useNativeFullscreen(element)

  const setGain = (index: number, db: number) => {
    const gains = settings.gains.map((g, i) => (i === index ? db : g))
    // Enabling on the first drag, rather than making the viewer find the switch
    // first: moving a slider is unambiguously a request to hear the difference,
    // and a control that does nothing until a second control is found is the
    // dead button the charter forbids.
    const next: EqSettings = { ...settings, gains, enabled: true, preset: 'custom' }
    onChange({ ...next, preset: identifyPreset(next) })
  }

  const setPreamp = (db: number) => {
    const next: EqSettings = { ...settings, preamp: db, enabled: true, preset: 'custom' }
    onChange({ ...next, preset: identifyPreset(next) })
  }

  const pickPreset = (name: PresetName) => {
    if (name === 'custom') return
    onChange(settingsForPreset(name))
  }

  return (
    <>
      <SettingRowLike
        label="Equalizer"
        on={settings.enabled}
        onToggle={() => onChange({ ...settings, enabled: !settings.enabled })}
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
                    'rounded-md px-2 text-xs font-medium transition-colors duration-150 ease-out',
                    tall ? 'py-2' : 'py-1.5',
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
                <input
                  type="range"
                  aria-label={`${band.label} hertz`}
                  min={-MAX_BAND_DB}
                  max={MAX_BAND_DB}
                  step={1}
                  value={settings.gains[i]}
                  onChange={(e) => setGain(i, Number(e.target.value))}
                  onDoubleClick={() => setGain(i, 0)}
                  className="h-24 w-4 accent-brand"
                  style={{ writingMode: 'vertical-lr', direction: 'rtl' }}
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
              className="min-w-0 flex-1 accent-brand"
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
    </>
  )
}

function formatDb(db: number): string {
  return `${db > 0 ? '+' : ''}${db}`
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
