import clsx from 'clsx'
import { useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { Sidebar } from '@/features/navigation/ui/Sidebar'
import { TopBar } from '@/features/navigation/ui/TopBar'

export function AppShell() {
  const { pathname } = useLocation()
  const isWatch = pathname.startsWith('/watch')
  const [expanded, setExpanded] = useState(true)

  // youtube.com hides the rail on the watch page to give the player room.
  const showFullSidebar = expanded && !isWatch
  const showMiniSidebar = !showFullSidebar && !isWatch

  return (
    <div className="min-h-dvh bg-bg">
      <TopBar onToggleSidebar={() => setExpanded((e) => !e)} />

      {showFullSidebar && <Sidebar mini={false} />}
      {showMiniSidebar && <Sidebar mini />}

      <main
        className={clsx(
          'transition-[margin] duration-200 ease-out',
          showFullSidebar && 'ml-60',
          showMiniSidebar && 'ml-[72px]',
        )}
      >
        <Outlet />
      </main>
    </div>
  )
}
