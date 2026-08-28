import { Navigate, Route } from 'react-router-dom'
import { ActivityPage } from '@/pages/ActivityPage'
import { ChannelPage } from '@/pages/ChannelPage'
import { HistoryPage } from '@/pages/HistoryPage'
import { HomePage } from '@/pages/HomePage'
import { SavedPage } from '@/pages/SavedPage'
import { WatchLaterPage } from '@/pages/WatchLaterPage'
import { PlaylistsPage } from '@/pages/PlaylistsPage'
import { PlaylistPage } from '@/pages/PlaylistPage'
import { SearchResultsPage } from '@/pages/SearchResultsPage'
import { SettingsPage } from '@/pages/SettingsPage'
import {
  AdvancedSettingsPage,
  FeedSettingsPage,
  NarrationSettingsPage,
  ProfileSettingsPage,
  YouTubeAccountPage,
  ProxySettingsPage,
  TranslationSettingsPage,
} from '@/pages/SettingsSectionPage'
import { SubscriptionsPage } from '@/pages/SubscriptionsPage'
import { StoragePage } from '@/pages/StoragePage'
import { WatchPage } from '@/pages/WatchPage'

/**
 * Every page the app has, written once.
 *
 * Once rather than twice, and that is the whole reason this is a module. On a
 * phone the watch screen is a layer *over* the page you came from, so that page
 * has to go on being rendered underneath — which means a second `<Routes>`
 * driven by a different location. Two route tables would be two things to keep
 * in step, and the one that drifted would only be noticed as a screen that
 * mysteriously came up blank behind the player.
 *
 * A fragment rather than an array: React Router reads `<Route>` children out of
 * the tree, fragments included, so the same JSX can be handed to a layout route
 * and to a bare `<Routes>` without either knowing about the other.
 */
export const pageRoutes = (
  <>
    <Route path="/" element={<HomePage />} />
    <Route path="/topic/:topicName" element={<HomePage />} />
    <Route path="/results" element={<SearchResultsPage />} />
    <Route path="/activity" element={<ActivityPage />} />
    <Route path="/history" element={<HistoryPage />} />
    <Route path="/saved" element={<SavedPage />} />
    <Route path="/watch-later" element={<WatchLaterPage />} />
    <Route path="/playlists" element={<PlaylistsPage />} />
    <Route path="/playlist/:playlistId" element={<PlaylistPage />} />
    <Route path="/subscriptions" element={<SubscriptionsPage />} />
    <Route path="/storage" element={<StoragePage />} />
    <Route path="/settings" element={<SettingsPage />} />
    {/* The account has a place of its own now, off Settings, because "who am I
        and what is my YouTube connection doing" is one subject and it was filed
        in two. The old paths still resolve: links are shared, and a bookmark
        that stops working is a worse answer than a redirect. */}
    <Route path="/profile" element={<ProfileSettingsPage />} />
    <Route path="/account" element={<YouTubeAccountPage />} />
    <Route path="/settings/profile" element={<Navigate to="/profile" replace />} />
    <Route
      path="/settings/youtube-account"
      element={<Navigate to="/account" replace />}
    />
    <Route path="/settings/feed" element={<FeedSettingsPage />} />
    <Route path="/settings/advanced" element={<AdvancedSettingsPage />} />
    <Route path="/settings/narration" element={<NarrationSettingsPage />} />
    <Route path="/settings/translation" element={<TranslationSettingsPage />} />
    <Route path="/settings/proxy" element={<ProxySettingsPage />} />
    <Route path="/channel/:channelId" element={<ChannelPage />} />
    <Route path="/watch/:videoId" element={<WatchPage />} />
    <Route path="*" element={<Navigate to="/" replace />} />
  </>
)
