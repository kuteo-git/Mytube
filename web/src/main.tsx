import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppShell } from './app/AppShell'
import { ActivityPage } from './pages/ActivityPage'
import { ChannelPage } from './pages/ChannelPage'
import { HistoryPage } from './pages/HistoryPage'
import { HomePage } from './pages/HomePage'
import { SavedPage } from './pages/SavedPage'
import { SearchResultsPage } from './pages/SearchResultsPage'
import { StoragePage } from './pages/StoragePage'
import { WatchPage } from './pages/WatchPage'
import './index.css'

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 30_000, refetchOnWindowFocus: false } },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<HomePage />} />
            <Route path="/topic/:topicName" element={<HomePage />} />
            <Route path="/results" element={<SearchResultsPage />} />
            <Route path="/activity" element={<ActivityPage />} />
            <Route path="/history" element={<HistoryPage />} />
            <Route path="/saved" element={<SavedPage />} />
            <Route path="/storage" element={<StoragePage />} />
            <Route path="/channel/:channelId" element={<ChannelPage />} />
            <Route path="/watch/:videoId" element={<WatchPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
