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
    mediaRootUnavailable: 'Không thấy ổ đĩa chứa video. Kiểm tra xem đã cắm chưa.',
    deleteSelf: 'Chuyển sang hồ sơ khác rồi mới xoá được hồ sơ này.',
    deleteLast: 'Đây là hồ sơ duy nhất. Thêm một hồ sơ khác rồi mới xoá được.',
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
      'Máy này đang là ai. Kênh đăng ký, lịch sử xem và gợi ý là của riêng từng người; kho video thì dùng chung.',    keepsSeparate: 'Giữ kênh đăng ký, lịch sử xem và gợi ý tách riêng cho từng người. Kho video thì dùng chung.',

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
    watched: 'Đã xem',
    unsave: 'Bỏ lưu',
    onYouTube: 'Trên YouTube',
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
      // Written as three sentences the way somebody would say it, not as one
      // long clause with three parentheses hanging off it.
      explainer:
        'Ba phần này chia nhau {{adjustable}}% của trang. Phần còn lại để dành cho video bạn đang xem dở ({{continue}}%), video đã xem xong mà có thể muốn xem lại ({{rewatch}}%), và video mới của các kênh bạn theo dõi ({{fresh}}%). Ba cái đó không phải là sở thích — hai cái đầu là lịch sử xem của bạn, cái cuối là cách bạn biết một kênh vừa đăng bài — nên không chia ở đây.',
      resetToDefault: 'Về mặc định',
      defaults: 'Mặc định',
      ofWindow: '{{count}} trên {{window}}',
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
      useBuiltIn: 'Dùng giá trị mặc định',
      careful: 'cẩn thận',
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

  player: {
    findingStream: 'Đang tìm nguồn phát…',    streamFailedDownloading:
      'Phát trực tiếp không được. Đang tải về thay thế — {{percent}}%, xong là tự chạy.',

    streamFailedQueued:
      'Phát trực tiếp không được. Video đang xếp hàng chờ tải sau một video khác, xong là tự chạy.',
    streamFailed: 'Không tải được luồng phát.',
    notStartedYet: 'Buổi phát chưa bắt đầu. Tới giờ là tự chạy.',
    evicted:
      'File đã bị xoá để lấy lại dung lượng, và trên mạng cũng không có nguồn nào phát thẳng được. Tải lại để xem.',
    nothingPlayable: 'Chưa có gì phát được. Phải tải xong đã.',
    noFile: 'Chưa có file nào.',
    streaming: 'Đang phát trực tiếp trong lúc tải bản lưu',
    copyQueued: 'Đã xếp hàng tải',
    downloadProgress: 'Tiến độ tải',
    nextVideo: 'Video tiếp theo',
    goToLive: 'Về chỗ đang phát',
    live: 'TRỰC TIẾP',
    // Kept in English: it is what the button is called on every device, and
    // the Vietnamese phrase is longer than the control it labels.
    pictureInPicture: 'Picture in picture',
    fullScreen: 'Toàn màn hình',
    expand: 'Mở rộng trình phát',
    closePlayer: 'Đóng trình phát',
    readAloud: 'Đọc to',
    vietnameseNarration: 'Lồng tiếng Việt',
    unavailable: {
      membersOnly:
        'Video này chỉ dành cho thành viên trên YouTube. Muốn xem thì tham gia kênh bên đó — không tải về thư viện được.',
      private: 'Video này để riêng tư trên YouTube nên không tải được.',
      removed: 'Video này đã bị gỡ khỏi YouTube nên không tải được.',
      generic: 'YouTube không cho lấy video này nên không tải được.',
    },
    speech: {
      notStarted: 'Chưa đọc',
      preparing: 'Đang chuẩn bị giọng đọc…',
      waitingTranslation: 'Đang chờ dịch…',
      unavailable: 'Dịch vụ đọc không phản hồi — đang thử lại',
      ready: 'Giọng đọc đã sẵn sàng',
    },
    translation: {
      notStarted: 'Chưa bắt đầu',
      waitingSettings: 'Đang chờ cài đặt dịch…',
      noModel: 'Chưa chọn model dịch — vào Cài đặt để chọn',
      readingSaved: 'Đang đọc bản dịch đã lưu…',
      loadingSubtitles: 'Đang tải phụ đề…',
      preparingCues: 'Đang chuẩn bị phụ đề…',
      noSubtitles: 'Không có phụ đề',
      alreadyVietnamese: 'Vốn đã là tiếng Việt — không cần dịch',      failedWith: 'Dịch không thành: {{error}}',

      failed: 'Dịch không thành — không nhận được gì',
    },
  },

  comments: {
    count: '{{count}} bình luận',
    placeholder: 'Viết bình luận...',
    label: 'Viết bình luận',
    noneReturned: 'YouTube không trả về bình luận nào cho video này.',
    couldNotLoad: 'Không tải được bình luận từ YouTube.',
    like: 'Thích bình luận',
    dislike: 'Không thích bình luận',
  },

  description: {
    showMore: '…thêm',
    // The server's enum, shown to a person. Rendered as words rather than as
    // the constant: "DOWNLOADING" is a value in a database, not a sentence.
    state: {
      ABSENT: 'Chưa tải',
      DOWNLOADING: 'Đang tải',
      READY: 'Đã có trên đĩa',
      EVICTED: 'Đã xoá để lấy chỗ',
      FAILED: 'Tải lỗi',
      UNAVAILABLE: 'YouTube không cho lấy',
    },
    onDisk: 'Trên đĩa',
    addedToLibrary: 'Thêm vào thư viện',
    mediaState: 'Trạng thái file',
    showLess: 'Thu gọn',
  },

  equalizer: {
    offInFullscreen: 'EQ không chạy khi toàn màn hình',
    offWhileDownloading: 'EQ không chạy khi video còn đang tải',
    alsoOffInFullscreen: 'Cũng không chạy khi toàn màn hình',
    alsoOffWhileDownloading: 'Cũng không chạy khi đang tải',
    preset: 'Cấu hình Equalizer',
    dryWet: 'Độ trộn khô/ướt',
  },

  queue: {
    machineVietnamese: 'Tiếng Việt (dịch máy) — dịch trong lúc xem',
    watchLater: 'Xem sau',
    topPlayed: 'Xem nhiều nhất',
  },

  youtubeAccount: {
    installStep: 'Cài <0>Get cookies.txt LOCALLY</0> cho Chrome — bản mà chính FAQ của yt-dlp khuyên dùng.',
    // The name is left in English because it is a name: the wrong extension
    // was pulled from the store as malware, and a translated name names
    // nothing.
    warning: 'Đừng cài "Get cookies.txt" mà thiếu chữ LOCALLY — bản đó đã bị gỡ khỏi store vì là mã độc.',
    disconnect: 'Ngắt kết nối',
    reconnect: 'Kết nối lại',
    summary: '{{subscriptions}} kênh đăng ký, {{playlists}} danh sách phát, {{videos}} video.',
    title: 'Tài khoản YouTube',
    description:
      'Lấy kênh đăng ký, danh sách phát và video đã thích của bạn về thư viện. Tài khoản của bạn, chỉ nằm trên máy này.',
    signedOut: 'Đã đăng xuất — dán lại cookie',
    notConnected: 'Chưa kết nối',
    howTo: 'Mở youtube.com khi đã đăng nhập, bấm vào tiện ích, chọn định dạng Netscape.',
    pasteBelow: 'Dán toàn bộ file xuống dưới.',
    cookiesFile: 'File cookie',
    scanNow: 'Quét ngay',
  },

  narration: {
    tryIt:
      'Mở một video rồi quay lại đây để nghe thử — trình phát vẫn chạy ở góc màn hình.',
    voiceVolume: 'Âm lượng giọng đọc',
    voiceVolumeHint: 'Vượt quá 100% được, vì giọng máy đọc nhỏ hơn tiếng phim.',
    videoVolumeWhileSpeaking: 'Âm lượng video khi đang đọc',
  },

  translationSettings: {
    description:
      'Nơi dịch phụ đề. Đổi model thì dịch lại từ đầu — bản dịch cũ vẫn giữ, nên đổi qua đổi lại không mất gì.',
    baseURL: 'Base URL',
    apiKey: 'API key',
    noKeyStored: 'Chưa lưu key nào.',
    hideKey: 'Ẩn API key',
    showKey: 'Hiện API key',
    couldNotLoadModels: 'Không tải được danh sách model.',
    testFailed: 'Gọi thử không tới nơi.',
    savedNextBatch: 'Đã lưu. Lượt dịch tới sẽ dùng cái này.',
  },

  upNext: {
    all: 'Tất cả',
    fromChannel: 'Từ {{name}}',
    nextIn: 'Tiếp theo: {{title}}',
    title: 'Tiếp theo',
    nothingQueued: 'Chưa có gì trong hàng chờ',
    expand: 'Mở danh sách tiếp theo',
    collapse: 'Thu danh sách tiếp theo',
    filter: 'Lọc gợi ý',
    playingFromQueue: 'Đang phát từ hàng chờ',
  },

  actions: {
    linkCopied: 'Đã chép link YouTube',
    couldNotCopy: 'Không chép được link',
  },

  storageMode: {
    fromEnvironment: 'Lấy từ biến môi trường — chưa lưu gì.',
    streamOnly: 'Chỉ xem trực tiếp, không lưu',
  },

  phoneSettings: {
    library: 'Thư viện',
    account: 'Tài khoản',
    preferences: 'Tuỳ chọn',
    settings: 'Cài đặt',
    narration: 'Lồng tiếng',
    translation: 'Dịch',
    advanced: 'Nâng cao',
  },

  ui: {
    // No plural: the English version had `job${count !== 1 ? 's' : ''}`, which
    // is a rule that produces "3 côngs việc" in a language that has none.
    jobsCleared_failed: 'Đã xoá {{count}} việc lỗi',
    jobsCleared_completed: 'Đã xoá {{count}} việc đã xong',
    percentOfBudget: '{{percent}}% giới hạn',
    writable: 'Ghi được. Còn trống {{free}}, đã có sẵn {{count}} video.',
    fits: 'Có {{count}} video hợp phần này.',
    viewMore: 'Xem thêm ({{count}})',
    playingFrom: 'Đang phát từ {{name}}',
    keyStored: 'Đã lưu một key kết thúc bằng {{hint}}. Để trống nếu muốn giữ nguyên.',
    videoCount: '{{count}} video',
    add: 'Thêm',
    adding: 'Đang thêm…',
    advanced: 'Nâng cao',
    audio: 'Âm thanh',
    auto: 'Tự động',
    autoplay: 'Tự phát',
    budget: 'Giới hạn',
    categories: 'Danh mục',
    check: 'Kiểm tra',
    checking: 'Đang kiểm tra…',
    comments: 'Bình luận',
    completed: 'Đã xong',
    connect: 'Kết nối',
    connected: 'Đã kết nối',
    dislike: 'Không thích',
    dismiss: 'Bỏ qua',
    downloads: 'Đang tải',
    // Apple's own terms for the two ends of a reverb mix, kept as they are on
    // the control: the Vietnamese words for them are longer than the slider.
    dryWet: 'Dry/Wet',
    environment: 'Không gian',
    evicted: 'Đã xoá',
    failed: 'Lỗi',
    kept: 'Đang giữ',
    like: 'Thích',
    main: 'Chính',
    model: 'Model',
    mute: 'Tắt tiếng',
    name: 'Tên',
    narration: 'Lồng tiếng',
    off: 'Tắt',
    pause: 'Tạm dừng',
    play: 'Phát',
    playlist: 'Danh sách phát',
    preamp: 'Preamp',
    profile: 'Hồ sơ',
    queue: 'Hàng chờ',
    refreshing: 'Đang làm mới',
    resolution: 'Độ phân giải',
    savedFull: 'Đã lưu.',
    saving: 'Đang lưu…',
    scanning: 'Đang quét…',
    scans: 'Lượt quét',
    search: 'Tìm kiếm',
    seek: 'Tua',
    seeking: 'Đang tua…',
    settings: 'Cài đặt',
    share: 'Chia sẻ',
    shared: 'Đã chia sẻ',
    status: 'Trạng thái:',
    subscribe: 'Đăng ký',
    subscribed: 'Đã đăng ký',
    subtitles: 'Phụ đề',
    test: 'Thử',
    testing: 'Đang thử…',
    translated: 'Đã dịch',
    translating: 'Đang dịch…',
    translation: 'Dịch',
    unmute: 'Bật tiếng',
    unsaved: 'Đã bỏ lưu',
    used: 'Đã dùng',
    verified: 'Đã xác minh',
    voice: 'Giọng đọc',
    volume: 'Âm lượng',
    whosWatching: 'Ai đang xem?',
  },

  empty: {
    what_playlists: 'danh sách phát',
    what_history: 'lịch sử xem',
    what_savedVideos: 'video đã lưu',
    what_watchLater: 'Xem sau',
    what_storageUsage: 'dung lượng ổ đĩa',
    history: 'Chưa xem video nào. Xem xong thì video sẽ hiện ở đây.',
    saved: 'Chưa lưu video nào. Lưu từ menu của video hoặc từ trang Ổ đĩa.',
    subscriptions: 'Chưa theo dõi kênh nào. Mở một kênh rồi bấm Đăng ký.',
    downloads: 'Chưa tải video nào. Bấm phát một video là nó tự xếp hàng tải về.',
    noEvictable:
      'Hiện không có video nào thuộc diện tự xoá. Video đã tải hoặc đang được giữ, hoặc vừa xem gần đây.',
    couldNotLoad: 'Không tải được {{what}}. Gateway có đang chạy không?',
    couldNotReachLibrary: 'Không kết nối được dịch vụ thư viện. Gateway có đang chạy không?',
  },

  storagePage: {
    fillsPast:
      'Khi ổ đĩa đầy quá {{budget}}, những video lâu chưa xem và không được giữ sẽ bị xoá khỏi đĩa. Thông tin và lịch sử xem thì vẫn còn.',
    bannerFull:
      'Ổ đĩa đã đầy {{percent}}% ({{used}} trên {{budget}}). Những video lâu chưa xem sẽ tự bị xoá khỏi đĩa; thông tin và lịch sử xem thì vẫn còn.',
    manage: 'Quản lý ổ đĩa',
    addSomeone: 'Thêm người',
  },

  more: {
    linesProgress: '{{done}}/{{total}} dòng',
    etaLeft: 'còn {{eta}}',
    // No plural: the English had `line${count === 1 ? '' : 's'}`.
    tooFastLines: '{{count}} dòng dài quá, không đọc kịp',
    milliseconds: '{{ms}} ms',
    loadingMore: 'Đang tải thêm',
    loadMore: 'Xem thêm',
    countInLibrary: 'có {{count}} trong thư viện',
    mixOf: 'Tuyển tập — {{count}} video bạn xem nhiều nhất',
    videoSuggestionCount: '{{count}} video',
    useBuiltInShort: 'dùng mặc định',
    pinnedByName: 'Được ghim bởi {{name}}',
    upNextInSeconds: 'Video tiếp theo sau {{seconds}}',
    scanLine: '{{sources}} nguồn · thấy {{seen}} video · thêm {{added}}',
    linesLeft: '{{done}}/{{total}} dòng',
    secondsLeft: 'còn {{seconds}}',
    lineTooLong: 'dòng {{index}} dài quá, không đọc kịp',
    newBadgeShort: 'Mới',
    savedByAnyone: 'có người lưu, không bao giờ bị xoá',
    lastScanResult: 'Quét lần cuối: {{result}}',
    inYourLibrary: 'trong thư viện',
    mix: 'Tuyển tập',
    mixOfWhat: 'Tuyển tập — {{count}} video bạn xem nhiều nhất',
    folder: 'Thư mục',
    videosAreAt: 'Có {{total}} video đang nằm ở {{path}}. Đổi thư mục không làm chúng chuyển theo — sẽ phải tải lại từ đầu.',
    keepThemFirst: 'Muốn giữ thì tự chuyển chúng sang trước, rồi mới đổi ở đây.',
    changeAnyway: 'Vẫn đổi',
    streamOnlyHint: 'Video phát thẳng từ YouTube, không tải về. Phụ đề vẫn có, file đã tải vẫn xem được, và nút Thử lại vẫn chạy.',
    lastScan: 'Quét lần cuối:',
    signedOutBanner: 'YouTube đã đăng xuất bạn — kênh đăng ký không còn được cập nhật.',
    lowerPreamp: 'Hạ preamp xuống nếu mấy dải được đẩy lên bị rè.',
    upNextIn: 'Video tiếp theo sau',
    pinnedBy: 'Được ghim bởi',
    reply: 'Trả lời',
    tryAgain: 'Thử lại',
    clearAll: 'Xoá hết',
    scanSummary: '{{sources}} nguồn · thấy {{seen}} video · thêm {{added}}',
    everythingShown: 'Đó là tất cả những gì trang chủ được đặt để hiện. <0>Chỉnh tỉ lệ</0> để mở rộng thêm.',
    nothingYetHome: 'Chưa có gì ở đây. Chủ đề được quét 12 tiếng một lần; bấm Làm mới để quét ngay. <0>Xem lại tỉ lệ trang chủ</0> — phần nào để 0% thì không hiện gì cả.',
    playlistUnopenable: 'Không mở được danh sách phát này. Có thể nó đã bị xoá, hoặc thuộc về tài khoản khác.',
    noPlaylistsYet: 'Chưa có danh sách phát nào. Kết nối tài khoản YouTube trong Cài đặt, lần quét sau chúng sẽ về.',
    linkNotVideo: 'Đường dẫn này không dẫn tới video hay kênh nào. Link danh sách phát thì chưa mở được ở đây.',
    resultsFor: 'Kết quả cho',
    onYouTube: 'Trên YouTube',
    modelsAvailable: 'Có {{count}} model.',
    videosCount: '{{count}} video',
    newBadge: 'Mới',
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
