import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Check } from 'lucide-react'
import clsx from 'clsx'

import { LANGUAGES, LANGUAGE_NAMES, setLanguage, type Language } from '@/shared/i18n'

import { Avatar } from '@/shared/ui/primitives'
import { hueFromId } from '@/shared/lib/hue'
import { useCurrentProfile, useProfiles } from '../application/use-profile'

/**
 * The avatar in the top bar, and what opens under it.
 *
 * It was a button with no handler and a hardcoded name — `<Avatar hue={210}
 * name="Luc" />` — so every member of the household saw Luc's initial, and
 * pressing it did nothing. A control that does not do what it says is the one
 * thing CLAUDE.md §5 and the design system's anti-patterns both forbid outright,
 * and this was the clearest example of it in the app.
 *
 * Switching profile is what people come here to do, far more often than
 * managing them, so the switch is the list itself rather than a page you travel
 * to first. Everything else is one row further down.
 */
export function AccountMenu() {
  const { t, i18n } = useTranslation()
  const language = i18n.language as Language
  const { data: profiles = [] } = useProfiles()
  const { id, choose } = useCurrentProfile()
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)

  const current = profiles.find((p) => p.id === id) ?? profiles[0]

  // Closing on an outside press or on Escape. Both, because a menu that can
  // only be dismissed by pressing its own button again is a menu that traps a
  // remote control — Phase 3 drives this with a D-pad.
  useEffect(() => {
    if (!open) return
    const away = (e: PointerEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false)
    }
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', away)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('pointerdown', away)
      document.removeEventListener('keydown', escape)
    }
  }, [open])

  return (
    <div ref={box} className="relative ml-1">
      <button
        type="button"
        aria-label="Account"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="grid place-items-center rounded-full"
      >
        <Avatar
          hue={hueFromId(current?.id ?? '')}
          name={current?.name ?? '?'}
          size={32}
        />
      </button>

      {open && (
        <div
          role="menu"
          // Fade and a 4px rise over 150ms — the design system's one dropdown
          // motion, and `motion-reduce` turns it off rather than shortening it.
          className="absolute top-full right-0 z-50 mt-2 w-60 origin-top-right rounded-xl
                     border border-line bg-surface py-2 shadow-lg
                     motion-safe:animate-[menu-in_150ms_ease-out]"
        >
          {profiles.map((p) => (
            <button
              key={p.id}
              type="button"
              role="menuitem"
              onClick={() => {
                // `choose` drops the whole query cache. Nearly everything here
                // is answered per member, and a leftover key would show one
                // person's shelf under another's name.
                if (p.id !== id) choose(p)
                setOpen(false)
              }}
              className={clsx(
                'flex w-full items-center gap-3 px-4 py-2 text-left text-sm transition-colors duration-150 ease-out hover:bg-surface-hover',
                p.id === id && 'font-medium',
              )}
            >
              <Avatar hue={hueFromId(p.id)} name={p.name} size={24} />
              <span className="min-w-0 flex-1 truncate">{p.name}</span>
              {p.id === id && <span className="text-xs text-text-2">{t('account.watching')}</span>}
            </button>
          ))}

          <hr className="my-2 border-0 border-t border-line" />

          <MenuLink to="/profile" onDone={() => setOpen(false)}>
            {t('account.manageProfiles')}
          </MenuLink>
          <MenuLink to="/account" onDone={() => setOpen(false)}>
            {t('account.youtubeAccount')}
          </MenuLink>

          <hr className="my-2 border-0 border-t border-line" />

          {/* Two rows rather than a submenu. There are exactly two languages,
              and hiding two rows behind a row is a press that buys nothing.

              Each named in its own language, always: somebody who switched by
              accident is now reading a interface they cannot navigate, and
              "English" written in English is the way back out. Translating
              these labels would close that door. */}
          {LANGUAGES.map((code) => (
            <button
              key={code}
              type="button"
              role="menuitemradio"
              aria-checked={language === code}
              onClick={() => {
                setLanguage(code)
                setOpen(false)
              }}
              className={clsx(
                'flex w-full items-center gap-3 px-4 py-2 text-left text-sm transition-colors duration-150 ease-out hover:bg-surface-hover',
                language === code && 'font-medium',
              )}
            >
              <Check
                size={16}
                className={clsx('shrink-0', language === code ? 'opacity-100' : 'opacity-0')}
              />
              <span>{LANGUAGE_NAMES[code]}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function MenuLink({
  to,
  onDone,
  children,
}: {
  to: string
  onDone: () => void
  children: React.ReactNode
}) {
  return (
    <Link
      to={to}
      role="menuitem"
      onClick={onDone}
      className="block px-4 py-2 text-sm transition-colors duration-150 ease-out hover:bg-surface-hover"
    >
      {children}
    </Link>
  )
}
