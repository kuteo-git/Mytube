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

  pages: {
    home: {
      continueWatching: 'Xem tiếp',
    },
    history: { title: 'Video đã xem' },
    saved: { title: 'Video đã lưu' },
    watch: { notFound: 'Không tìm thấy video.' },
    channel: {
      notFound: 'Không tìm thấy kênh.',
      sort: 'Sắp xếp video',
      unreachable: 'Không kết nối được YouTube để lấy danh sách của kênh này.',
      noUploads: 'Kênh này chưa đăng video nào.',
    },
    search: {
      fromLink: 'Video từ một đường dẫn',
      inLibrary: 'Có trong thư viện',
      noMatches: 'Không có gì khớp.',
      youtubeUnreachable: 'Không kết nối được YouTube.',
      noMore: 'Hết kết quả.',
    },
    activity: {
      historyCleared: 'Đã xoá lịch sử quét',
      inProgress: 'Đang chạy',
      scanNow: 'Quét ngay',
      neverScanned: 'Chưa quét lần nào.',
      retryDownload: 'Tải lại',
      cancelDownload: 'Huỷ tải',
      queued: 'Đang chờ tới lượt',
    },
    storage: {
      softCeiling: 'Ngưỡng bắt đầu tự xoá',
      freeOnDisk: 'Còn trống',
      videosOnDisk: 'Video trên đĩa',
      nextRemoved: 'Sẽ xoá tiếp',
    },
    watchLater: {
      title: 'Xem sau',
      // "signed you out" — the passive would be stiff in Vietnamese; this says
      // who did what, which is how somebody would tell you.
      signedOut:
        'YouTube đã đăng xuất bạn nên danh sách này không được cập nhật nữa. Dán lại cookie trong phần Cài đặt.',
      empty:
        'Chưa có gì ở đây. Đây là bản sao danh sách Xem sau trên YouTube, được lấy về mỗi lần quét tài khoản.',
    },
    playlists: {
      wontOpen: 'YouTube không mở được cái này',
      waitingSession: 'Đang chờ phiên đăng nhập YouTube',
      notReadYet: 'Chưa đọc',
      wontOpenLong:
        'YouTube có liệt kê danh sách này nhưng không mở được — nó báo "danh sách phát không tồn tại". Không có cách nào sửa từ đây; đã hỏi một lần rồi thôi.',
      notReadYetLong:
        'Danh sách này chưa được lấy về từ YouTube. Nó sẽ đầy dần ở một trong những lần quét tài khoản sắp tới.',
      emptyUpstream: 'Danh sách này trống trên YouTube.',
    },
  },

  account: {
    openMenu: 'Tài khoản',
    // Not "Đang xem" — that means watching a video. This marks which profile
    // the browser is currently using.
    watching: 'Đang dùng',
    manageProfiles: 'Quản lý hồ sơ',
    youtubeAccount: 'Tài khoản YouTube',
  },
}
