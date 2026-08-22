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

  search: {
    placeholder: 'Tìm trong thư viện',
    clear: 'Xoá ô tìm kiếm',
    toggleSidebar: 'Ẩn/hiện menu',
  },

  profiles: {
    // "Who this browser is" — a Vietnamese sentence would not start with a
    // question word here; it states what the setting is for.
    whoIsThis:
      'Máy này đang là ai. Kênh đăng ký, lịch sử xem và gợi ý là của riêng từng người; kho video thì dùng chung.',
    enterName: 'Nhập tên',
    couldNotAdd: 'Không thêm được tên này',
    newProfileName: 'Tên hồ sơ mới',
    deleteTitle: 'Xoá {{name}}?',
    counting: 'Đang đếm những gì thuộc về hồ sơ này…',
    couldNotRead: 'Không đọc được những gì thuộc về hồ sơ này.',
    couldNotDelete: 'Không xoá được hồ sơ này.',
    removesFor: 'Sẽ xoá, chỉ của {{name}}:',
    nothingYet: 'Hồ sơ này chưa xem hay lưu gì cả.',
    // The reassurance matters as much as the warning, so it is written rather
    // than converted: "video và kênh thì vẫn còn" is how somebody would say it.
    librarySurvives:
      'Video và kênh thì vẫn còn — chúng là của cả nhà. Xoá rồi không lấy lại được.',
    deleting: 'Đang xoá…',
    counts: {
      subscriptions: 'kênh đăng ký',
      watched: 'video đã xem',
      playlists: 'danh sách phát',
      reactions: 'lượt thích và không thích',
      saved: 'video đã lưu',
      watchLater: 'trong Xem sau',
      comments: 'bình luận',
    },
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

  settings: {
    feedMix: {
      title: 'Trang chủ',
      couldNotRead:
        'Không đọc được tỉ lệ hiện tại. Có thể gateway đang chạy bản cũ chưa có cài đặt này.',
      // "add up to one page" — Vietnamese says it as sharing one page, which is
      // clearer than the arithmetic image.
      intro:
        'Video mới trên trang chủ lấy từ đâu. Ba phần này chia nhau một trang, nên tăng phần này là giảm hai phần kia.',
      subscribed: 'Kênh bạn theo dõi',
      affinity: 'Thêm thứ bạn hay xem',
      affinityHint: 'Kênh bạn chưa đăng ký, về những chủ đề bạn hay quay lại.',
      discovery: 'Thứ gì đó mới',
      discoveryHint: 'Ngoài những chủ đề quen thuộc. Để 0 thì không hiện cái nào.',
      savedRebuilt: 'Đã lưu — trang chủ đã dựng lại.',
      couldNotSave: 'Không lưu được. Gateway có đang chạy không?',
      emptyBucket: 'Hiện thư viện không có gì hợp phần này, nên chỗ của nó dồn cho hai phần kia.',
      thin: 'Chỉ có {{count}} video hợp phần này, nên để tỉ lệ lớn thì hoặc lặp lại chúng, hoặc phải với sâu xuống cuối danh sách.',
    },    ranking: {
      sessionBlend: {
        label: 'Bám theo thứ bạn đang xem',
        hint: 'Vài video trong buổi xem này nặng bao nhiêu so với cả lịch sử xem. Để 0 thì trang chủ bỏ qua hôm nay; kéo hết thì gần như thành thanh Xem tiếp.',
      },
      freshSubscribed: {
        label: 'Chỗ dành cho video mới đăng',
        hint: 'Một phần mỗi trang để dành cho video các kênh bạn theo dõi vừa đăng, để video mới không phải tranh chỗ bằng điểm số.',
      },
      freshnessWindow: {
        label: 'Bao lâu thì một video còn được coi là mới',
        hint: 'Cả phần để dành ở trên lẫn phần đẩy tin nóng lên đều dùng con số này.',
      },
      maxAge: {
        label: 'Video cũ nhất mà trang chủ còn hiện',
        hint: 'Video cũ hơn vẫn tìm được qua ô tìm kiếm và trang kênh; chỉ là không chiếm chỗ trên trang chủ.',
      },
      recencyHalfLife: {
        label: 'Video mới tải về mờ đi nhanh cỡ nào',
        hint: 'Video vừa tải về sẽ nằm đầu trang rồi lắng dần. Đây là thời gian để nó mất một nửa phần ưu tiên đó.',
      },
      temperature: {
        label: 'Thứ tự bám sát điểm số cỡ nào',
        hint: 'Thấp thì video điểm cao luôn nằm trên; cao thì mấy video điểm sát nhau đổi chỗ cho nhau giữa các lần vào. Gần 0 thì lần nào mở trang cũng y hệt.',
      },
      poolSize: {
        label: 'Bao nhiêu video được đưa vào bốc',
        hint: 'Chỉ chừng này video mỗi phần được xáo, phần còn lại giữ nguyên thứ tự điểm. Kéo lên cao chính là thứ từng để video điểm âm lọt lên trang đầu.',
      },
      unit: {
        thisSitting: '{{value}}% buổi này',
        ofPage: '{{value}}% mỗi trang',
        hours: '{{value}} giờ',
        days: '{{value}} ngày',
        months: '{{value}} tháng',
        years: '{{value}} năm',
        videos: '{{value}} video',
        plain: '{{value}}',
      },
    },

    advanced: {
      couldNotRead:
        'Không đọc được cài đặt xếp hạng. Có thể gateway đang chạy bản cũ chưa có mấy cái này.',
      intro:
        'Cách trang chủ xếp hạng, chứ không phải nó gồm những gì. Cái nào bạn không đặt thì dùng giá trị mặc định, nên để nguyên là nó tự theo kịp khi bộ xếp hạng thay đổi.',
      breaks: 'Kéo xa tới đây có thể làm thứ tự hỏng thấy rõ.',
      builtIn: 'Mặc định: {{value}}',
    },
    storage: {
      title: 'Thư mục thư viện',
      description: 'Nơi lưu video đã tải, và có lưu hay không.',
      couldNotRead: 'Không đọc được cài đặt ổ đĩa.',
      couldNotReach: 'Không kết nối được máy chủ để kiểm thư mục đó.',
      savedRestart:
        'Đã lưu. Khởi động lại app thì mới có tác dụng — ba service đọc cái này lúc khởi động.',
      couldNotSaveFolder: 'Không lưu được thư mục đó.',
      couldNotChange: 'Không đổi được cài đặt này.',
      savedHere: 'Đã lưu ở đây.',
    },
    model: {
      name: 'tên model',
      chooseFromList: 'Chọn từ danh sách',
      noMatch: 'không khớp',
      reload: 'Tải lại danh sách model',
      type: 'Gõ tên model',
    },
    couldNotSave: 'Không lưu được',
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
