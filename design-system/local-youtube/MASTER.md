# Design System Master — Local YouTube

> **LOGIC:** Khi build một page cụ thể, đọc `design-system/local-youtube/pages/[page].md` trước.
> Nếu file đó tồn tại, rule của nó **override** file này. Nếu không, theo đúng file này.

**Project:** Local YouTube · **Category:** Content-dense application shell (KHÔNG phải landing page)
**Dials:** Variance 2/10 (Centered/Minimal) · Motion 3/10 (Subtle) · Density 8/10 (Dense)

> ⚠️ **Nguồn sự thật là `Example/home.png` và `Example/play.png`.** Mục tiêu là clone pixel-perfect
> YouTube dark theme. Token dưới đây đo từ 2 ảnh đó. Không thay bằng gợi ý generic từ DB —
> output gốc của ui-ux-pro-max (style "Exaggerated Minimalism", accent `#E11D48`, pattern
> "Video-First Hero + CTA") là design system cho landing page marketing và **đã bị loại bỏ có chủ đích**.

---

## 1. Color tokens (dark theme — mặc định và duy nhất ở Phase 1)

| Role | Hex | CSS var | Dùng ở đâu |
|---|---|---|---|
| Background | `#0F0F0F` | `--bg` | Nền toàn trang, topbar, sidebar |
| Surface raised | `#212121` | `--surface` | Menu dropdown, chip, tooltip, banner |
| Surface hover | `#272727` | `--surface-hover` | Hover của sidebar item, icon button, chip |
| Surface input | `#121212` | `--surface-input` | Ô search |
| Border | `#303030` | `--border` | Viền search, divider sidebar |
| Border subtle | `rgba(255,255,255,0.10)` | `--border-subtle` | Divider trong description/comment |
| Text primary | `#F1F1F1` | `--text` | Tiêu đề video, tên kênh, nav label |
| Text secondary | `#AAAAAA` | `--text-2` | Lượt xem, thời gian, mô tả phụ |
| Link | `#3EA6FF` | `--link` | Hashtag, URL trong description, "Update" |
| Brand red | `#FF0000` | `--brand` | Logo, badge LIVE, progress bar đã xem |
| Invert surface | `#F1F1F1` | `--invert-bg` | Nền nút Subscribe, chip active |
| Invert text | `#0F0F0F` | `--invert-text` | Chữ trên nút Subscribe, chip active |
| Focus ring | `#1C62B9` | `--ring` | Viền search khi focus |
| Overlay badge | `rgba(0,0,0,0.80)` | `--badge-bg` | Badge thời lượng trên thumbnail |

**Nguyên tắc:** không dùng raw hex trong component. Chỉ dùng CSS variable / Tailwind theme token.
Accent duy nhất là **đỏ YouTube** — không thêm màu thứ hai.

---

## 2. Typography

**Font: Roboto** (KHÔNG dùng Inter — sai x-height, clone sẽ "gần giống mà sai").
```css
@import url('https://fonts.googleapis.com/css2?family=Roboto:wght@400;500;700&display=swap');
```
Self-host qua `@fontsource/roboto` để chạy offline trong LAN (không phụ thuộc Google CDN).

| Token | Size / Weight / LH | Dùng ở đâu |
|---|---|---|
| `--t-card-title` | 14px / 500 / 20px | Tiêu đề video trong grid — **clamp 2 dòng** |
| `--t-meta` | 12px / 400 / 18px | Tên kênh, views, thời gian |
| `--t-nav` | 14px / 400 / 20px | Sidebar item |
| `--t-section` | 16px / 500 | Nhóm "Explore", "More from" |
| `--t-watch-title` | 20px / 700 / 28px | Tiêu đề trên watch page |
| `--t-body` | 14px / 400 / 20px | Description, comment |
| `--t-btn` | 14px / 500 | Nhãn nút |
| `--t-chip` | 14px / 500 | Chip filter |

Body base 14px — thấp hơn khuyến nghị 16px chung, **nhưng đây là chủ ý**: YouTube dùng 14px và đây là app content-dense. Bù lại bằng contrast cao (`#F1F1F1` trên `#0F0F0F` = 17.9:1).

---

## 3. Spacing & layout (đo từ reference)

| Token | Value |
|---|---|
| `--space-xs` | 4px |
| `--space-sm` | 8px |
| `--space-md` | 12px |
| `--space-lg` | 16px |
| `--space-xl` | 24px |
| `--space-2xl` | 40px |

| Đại lượng | Giá trị |
|---|---|
| Topbar height | **`--top-bar` = 56px + `--safe-top`**, `absolute` đè lên vùng cuộn, nền `.chrome-blur` |
| Sidebar mở | **240px** · Sidebar thu gọn | **72px** (icon + label nhỏ) |
| Grid gap | **16px** ngang · **40px** dọc |
| Thumbnail | ratio **16:9**, `border-radius: 12px` |
| Avatar | **36px** trong card · **40px** watch page · **24px** comment |
| Chip | height **32px**, radius **8px**, padding `0 12px` |
| Icon button | **40px** hit area, icon 24px, radius full |
| Watch: cột trái | fluid, max **1280px** · cột phải **402px**, gap **24px** |
| Nút Subscribe | height **36px**, radius full |

**Breakpoint grid:** ≥1600px → 4 cột · 1000–1600 → 3 cột · 700–1000 → 2 cột · <700 → 1 cột.
Watch page <1000px → sidebar "Next" xuống dưới comment.

---

## 4. Component specs

**Video card (grid):** thumbnail (radius 12, badge duration góc dưới-phải `--badge-bg` radius 4, progress bar đỏ nếu xem dở) → hàng meta: avatar 36px + khối text (title 2 dòng clamp / channel / `views · time`) + nút 3 chấm **chỉ hiện khi hover hoặc focus**.
Hover card: **không transform, không shadow, không scale** — YouTube không làm vậy, và scale gây layout shift.

**Sidebar item:** height 40px, radius 10px, padding trái 12px, icon 24px cách label 24px. Hover `--surface-hover`. Active: nền `--surface-hover` + label weight 500.

**Chrome trong suốt (sửa 2026-08-05 — lệch có chủ đích khỏi ảnh tham chiếu):** nền `--bg` ở
**95%** phủ blur 24px + saturate 150%, **không** đục như `Example/home.png`. Viết một lần thành
class **`.chrome-blur`** trong `index.css`, dùng cho **cả ba** mặt: top bar, ChipBar, và thanh
player trên mobile. Alpha là con số phải chỉnh bằng mắt; để thành nhiều literal thì các mặt sẽ
lệch nhau và không ai nói được cái nào mới đúng ý.

Lý do lệch khỏi ảnh gốc: trang cuộn bên trong `<main>` chứ không cuộn cửa sổ (xem `CLAUDE.md`),
và bar **đè lên** vùng cuộn — nội dung đi *phía sau* nó. Đục thì phần đè đó vô nghĩa.

**Nền bar tràn lên phủ vùng an toàn trên cùng**, nội dung nằm trong 56px bên dưới — đúng kiểu
`BottomNav` đã làm với `--safe-bottom`. Không làm vậy thì trên máy có tai thỏ logo và ô search
nằm dưới đồng hồ, và mặt blur dừng sớm để lại một dải phẳng phía trên nó.
Chiều cao tổng viết một lần thành **`--top-bar`** (`index.css`), vì có tới sáu thứ phải bắt đầu
bên dưới bar — scroller, sidebar mini, sidebar đầy, drawer, scrim, ChipBar — và mỗi chỗ tự cộng
lấy là mỗi chỗ có thể cộng sai. Đây đã là **lần thứ tư** trong dự án cùng một lỗi: đếm chiều cao
TopBar hai lần.

> **Alpha ở đây là phần của lớp TỐI, nên đọc ngược: tăng alpha = tối đi.** Bắt đầu ở 70% và bị báo
> là quá sáng — một trang đầy thumbnail sáng cuộn phía sau làm cả bar bừng lên và đẩy chữ ra
> trước. Qua 85%, qua 90%, chốt **95%** — gần như đục, và blur gần như chỉ còn một gợi ý. Đó mới
> là thứ cần: dấu hiệu rằng có gì đó đang chuyển động phía sau, không phải một ô cửa nhìn vào nó.

**Đường nối giữa hai lớp blur chồng nhau: có thật, nhưng ở 95% thì không đáng.** Hai
`backdrop-filter` cạnh nhau không bao giờ khớp — mỗi lớp làm mờ backdrop *của riêng nó* rồi cắt
theo biên của chính nó, nên dọc mép giáp ranh hai bên bịa pixel từ hai vùng khác nhau. Đó là hạn
chế của **kỹ thuật**, không phải tham số. Nhưng ở 95% chỉ còn một phần hai mươi nội dung lọt qua,
nên cũng chỉ còn chừng ấy biên độ lệch để mà thấy. Phản đối này đúng ở 70% và không còn đúng ở
đây; ChipBar từng để đục vì lý do đó, giờ dùng chung `.chrome-blur`.
Chip vẫn là **pill đục** (`--surface` / `--invert-bg`), nên chữ không bao giờ nằm trên gì khác
ngoài nền đục — chỉ dải phía sau chúng là trong.

**ChipBar dính ở `top-0`, KHÔNG phải `top-14`.** Ngưỡng sticky đo từ mép content của vùng cuộn, mà
vùng cuộn đã mang sẵn `pt-14`. Ghi thêm chiều cao bar là đếm hai lần → ngưỡng 112px, và sticky
**đẩy phần tử xuống** cho tới ngưỡng, nên hàng chip tụt đúng một header ngay cả khi chưa cuộn.
Con số 56 chỉ được ghi ở **một** chỗ: vùng cuộn.

**Miniplayer:** blur **chỉ ở biến thể `bar` trên mobile**, nơi video chỉ rộng 128px nên tiêu đề và
nút nằm trên nền của chính host. Miniplayer desktop thì video phủ kín khung: blur ở đó bị che hoàn
toàn, chỉ tốn thêm một lớp compositing.

**Padding ngang của ChipBar nằm trên VÙNG CUỘN, không nằm trên wrapper.** Đặt ở wrapper thì nó
rút ngắn chính vùng cuộn, chip biến mất trước mép màn hình 16px và hàng đọc như bị cắt cụt thay
vì đang tiếp diễn. Kèm `scroll-px-4` để khi *trình duyệt* cuộn hàng này (nút mũi tên, focus bàn
phím) chip cũng dừng cách mép đúng chừng đó.

**Miniplayer:** blur **chỉ ở biến thể `bar` trên mobile**, nơi video chỉ rộng 128px nên tiêu đề
và nút nằm trên nền của chính host. Miniplayer desktop thì video phủ kín khung — blur ở đó bị che
hoàn toàn, chỉ tốn thêm một lớp compositing.
**Vị trí dính: `top-0`, KHÔNG phải `top-14`.** Ngưỡng sticky đo từ mép content của vùng cuộn, mà
vùng cuộn đã mang sẵn `pt-14`. Ghi thêm chiều cao bar là đếm hai lần → ngưỡng 112px, và sticky
**đẩy phần tử xuống** cho tới ngưỡng, nên hàng chip tụt đúng một header ngay cả khi chưa cuộn.
Con số 56 chỉ được ghi ở **một** chỗ: vùng cuộn.

**Search:** input nền `--surface-input`, border `--border`, radius full trái; nút search nền `--surface` radius full phải; focus → border `--ring`.

**Chip:** mặc định nền `--surface` text `--text`; active nền `--invert-bg` text `--invert-text`. Thanh chip scroll ngang, có nút mũi tên 2 đầu, **ẩn scrollbar**.

**Button (Like/Share/Save):** pill, nền `--surface`, height 36px, hover `--surface-hover`. Like/Dislike là **segmented** dính nhau, ngăn bằng divider 1px.

---

**Bottom bar (mobile) — 2026-08-05:** `Home · Subscriptions · History · Settings`.
Chỉ chứa **nơi bạn đi qua lại** khi đang duyệt. Mọi thứ khác là **màn bạn tới có chủ đích**, và
màn như vậy thì có `BackBar` riêng, không search header, không tab bar: Saved, Storage, Activity,
trang kênh, và từng mục Settings (`/settings/feed`, `/settings/narration`, `/settings/translation`).
Settings trên mobile là **danh sách hàng**, không phải ba khối slider xếp dọc — trên 390px thì đó
là trang phải cuộn tìm đúng một control mình cần, mà nó không bao giờ nằm trên cùng.
Panel trên màn riêng **bỏ tiêu đề của nó** (`headless`), vì `BackBar` đã nêu tên rồi.
**Đúng 5, và đó là trần**: quá 5 thì mỗi target tụt dưới 44px. Nên **mỗi lần thêm là một lần
bớt** — Storage nhường chỗ cho Settings, rồi Activity nhường chỗ cho Subscriptions. Cả hai dời
lên **đầu trang Settings**, không phải cuối: chúng không phải tuỳ chọn mà là trang người ta tìm
tới khi có chuyện, chôn dưới ba khối slider là ngược với việc đang làm.

**Màn drill-in trên mobile (`/channel/*`, và màn watch):** bỏ cả search header lẫn bottom bar,
thay bằng **`BackBar`** — nút back cùng `.chrome-blur`, cùng `--top-bar`. **Tiêu đề hiện dần**:
lúc đầu chỉ có mũi tên, cuộn qua khỏi `ChannelHeader` thì tên kênh mới mờ vào. Vì header đã nêu
tên bằng chữ lớn rồi; thanh nêu lại là nói cùng một điều hai lần cách nhau một inch. Dùng
IntersectionObserver, không dùng scroll handler — cùng lý do với miniplayer.
Trang kênh **không phải lớp phủ** như màn watch: không có gì bên dưới cần sống tiếp, và vị trí
cuộn lúc back đã có sẵn cơ chế lo (POP → khôi phục theo entry, vì `/channel/*` không nằm trong
`TAB_ROOTS`).

**Pull to refresh (Home, mobile only — 2026-08-05):** kéo xuống từ **đỉnh trang** để nạp lại feed.
Ngưỡng **72px quãng đường trang** (≈120px quãng đường ngón tay, đúng tầm app native), trần 110px.
Đường cong: **1:1 lúc đầu rồi nặng dần** — bám sát ngay để biết là đã nhận cử chỉ, rồi nặng lên để
tay biết đã tới giới hạn.
Chỉ báo **nói trước khi thả**: mũi tên xoay 180° khi đủ ngưỡng. Đó là khác biệt giữa một cử chỉ có
thể bỏ giữa chừng và một cử chỉ chỉ có thể hối tiếc.
Làm mới **toàn bộ trang** (feed + history + top-played + storage), không chỉ lưới — làm mới nửa
trang thì nửa không đổi lại chính là nửa người ta để ý.
**Desktop không có**: đã có bàn phím và nút reload, thêm cử chỉ là cách thứ ba cho cùng một việc.

---

## 5. Motion — mức Subtle (3/10)

- Transition chuẩn **150ms `ease-out`**, chỉ trên `background-color`, `opacity`, `transform`.
- **Không** GSAP, **không** scroll-reveal. Đây là app shell, không phải landing page — nội dung phải có mặt ngay.
- Sidebar collapse: `width` 200ms. Menu dropdown: fade + `translateY(-4px)` 150ms.
- **Bắt buộc** tôn trọng `prefers-reduced-motion: reduce` → `transition: none`.

---

## 6. Anti-patterns (CẤM)

- ❌ **Emoji làm icon** — dùng SVG. Icon set: **Lucide** (gần YouTube nhất), một bộ duy nhất.
- ❌ **Scale/shadow khi hover card** — gây layout shift, và không giống YouTube.
- ❌ **shadcn/ui** — đã loại ở charter, ngôn ngữ thiết kế xung đột.
- ❌ **Scroll-reveal / animation trang trí** — nội dung phải hiện ngay.
- ❌ **Raw hex trong component** — chỉ token.
- ❌ **Nút chết** — xem `CLAUDE.md` §5, mọi phần tử phải có chức năng thật.
- ❌ **Chỉ dựa vào hover** — Phase 3 chạy trên TV bằng remote, mọi hành động phải tới được bằng focus.
- ❌ Ẩn focus ring.

---

## 7. Pre-delivery checklist

- [ ] Icon toàn bộ từ Lucide, không emoji
- [ ] `cursor-pointer` trên mọi phần tử bấm được
- [ ] Transition 150ms trên hover, không transform gây shift
- [ ] Contrast ≥ 4.5:1 (dark theme — kiểm tra `--text-2 #AAA` trên `--bg`: đạt 7.4:1)
- [ ] **Focus ring nhìn thấy rõ** — không chỉ để a11y, mà vì Phase 3 điều hướng bằng D-pad
- [ ] Mọi hành động tới được bằng bàn phím (Tab/Enter), không có thứ chỉ hover mới dùng được
- [ ] `prefers-reduced-motion` được tôn trọng
- [ ] Responsive kiểm ở 375 / 768 / 1024 / 1440 / 1920px
- [ ] Không horizontal scroll ở bất kỳ breakpoint nào
- [ ] Thumbnail có `width`/`height` hoặc `aspect-ratio` → **CLS < 0.1**
- [ ] Ảnh `loading="lazy"` + `decoding="async"`
- [ ] Grid dài: virtualize hoặc infinite scroll theo trang (TanStack Query `useInfiniteQuery`)
- [ ] Font Roboto self-host, không gọi Google CDN (phải chạy được offline trong LAN)
