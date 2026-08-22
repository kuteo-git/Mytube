import type { Dictionary } from './en'

/**
 * Tiếng Việt.
 *
 * Typed as `Dictionary`, so a key that exists in English and not here is a
 * compile error rather than a word that quietly renders in English.
 *
 * ## How this is written
 *
 * Not converted sentence by sentence. Vietnamese drops the subject wherever the
 * context carries it, and English does not — so "they belong to the whole
 * household. This cannot be undone" becomes "chúng là của cả nhà. Xoá rồi
 * không lấy lại được", which says the same thing in the way somebody would
 * actually say it. Carrying every "they", "this" and "it" across is the surest
 * sign of a machine.
 *
 * The register is "bạn", and it is usually left out too: "Videos you saved"
 * is "Video đã lưu", not "Các video mà bạn đã lưu".
 *
 * Technical names stay in English — Equalizer, HLS, Remux, Reverb, Preamp.
 * Somebody opening the equaliser already knows the word, and "bộ chỉnh âm"
 * teaches nobody anything. The sentences around them are translated.
 */
export const vi: Dictionary = {
  common: {
    cancel: 'Huỷ',
    save: 'Lưu',
    saved: 'Đã lưu',
    retry: 'Thử lại',
    delete: 'Xoá',
    loading: 'Đang tải…',
    loadMore: 'Xem thêm',
    close: 'Đóng',
    back: 'Quay lại',
    moreOptions: 'Tuỳ chọn khác',
    moreActions: 'Thao tác khác',
  },

  nav: {
    home: 'Trang chủ',
    saved: 'Đã lưu',
    history: 'Đã xem',
    storage: 'Ổ đĩa',
    settings: 'Cài đặt',
    activity: 'Hoạt động',
    subscriptions: 'Kênh đăng ký',
    youtubeAccount: 'Tài khoản YouTube',
    watchLater: 'Xem sau',
    playlists: 'Danh sách phát',
    account: 'Tài khoản',
    toggleSidebar: 'Ẩn/hiện menu',
    searchPlaceholder: 'Tìm trong thư viện',
    clearSearch: 'Xoá ô tìm kiếm',
    homeFeed: 'Trang chủ',
    topics: 'Chủ đề',
    profile: 'Hồ sơ',
  },

  chips: {
    all: 'Tất cả',
    live: 'Trực tiếp',
    scrollLeft: 'Cuộn danh mục sang trái',
    scrollRight: 'Cuộn danh mục sang phải',
  },

  card: {
    live: 'TRỰC TIẾP',
    suggested: 'Gợi ý',
    markWatched: 'Đánh dấu đã xem',
    markedWatched: 'Đã đánh dấu là đã xem',
    notInterested: 'Không quan tâm',
    removed: 'Đã xoá khỏi đĩa — bấm để tải lại',
    couldNotOpen: 'Không mở được video này.',
  },

  language: {
    label: 'Ngôn ngữ',
  },
}
