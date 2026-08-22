# Hướng dẫn sử dụng

Mỗi màn hình dùng để làm gì, và một số hành vi cố ý nhưng dễ gây bất ngờ.

**English: [USAGE.md](USAGE.md)**

---

## Đưa video vào

**Dán link.** Dán URL YouTube vào ô tìm kiếm thì nó tải thẳng chứ không đem đi
tìm. Thư viện được hỏi trước theo id, nên video đã có sẵn sẽ mở ra mà không cần
chạm tới mạng.

**Tìm kiếm.** Kết quả tách làm hai nhóm — *Có trong thư viện* và *Trên YouTube*.
Thứ ở nhóm sau được tải về khi bạn mở nó.

**Theo dõi kênh.** Bấm Đăng ký ở trang kênh thì nó thành một nguồn sống: video
mới tự về, và có một lượt quét nhanh 5 phút một lần cho các kênh đã theo dõi, lấy
những gì đăng trong 48 giờ.

**Sửa `topics.yaml`.** File ở thư mục gốc là danh sách nguồn chính, đọc mỗi
tiếng. Muốn trang chủ nói về chủ đề nào thì sửa file này.

---

## Xem

Bấm phát là video chạy **trước khi** tải xong. Mấy giây đầu bạn đang xem chính
các track adaptive của YouTube, được mô tả thành playlist để trình duyệt tự ghép.
Khi file về tới nơi — trung vị **13 giây** trên máy này — player chuyển sang bản
trên đĩa mà không làm gián đoạn.

Nhãn ở góc trên trái cho biết đang ở tier nào.

| Điều khiển | Ghi chú |
|---|---|
| Chất lượng | Tự động giữ 720p khi đang stream. Ghim 1080p là một mệnh lệnh, và nó giữ nguyên. |
| Phụ đề | Những gì video có sẵn, cộng bản dịch tiếng Việt nếu bạn bật. |
| Đọc to | Đọc phụ đề tiếng Việt chồng lên video. Cần endpoint giọng nói — xem [Cài đặt](SETUP.vi.md#7-tuỳ-chọn-lồng-tiếng). |
| Âm thanh | Equalizer 10 dải và bốn không gian. Theo từng máy, vì nó chỉnh cho **loa của bạn**. |
| Tự phát | Dừng sau ba video liên tiếp không ai đụng vào. |

**Tua được ở mọi tier.** Với buổi phát trực tiếp, thanh tua phủ đúng cửa sổ xem
lại mà YouTube cho — khoảng một tiếng — và nút **TRỰC TIẾP** đưa bạn về mép đang
phát.

### Mấy thứ trông như lỗi mà không phải

- **Trên iPhone video không tự phát.** Safari đòi một cử chỉ trước khi cho ra
  tiếng. Nó đứng ở khung hình đầu chứ không tự tắt tiếng để phát.
- **Equalizer không ăn trên điện thoại khi video còn đang tải.** iOS không đưa
  âm thanh HLS qua Web Audio bằng bất kỳ đường nào — đã đo, có lẫn không có thư
  viện hỗ trợ. Bảng điều khiển nói rõ trong lúc đó, và nó hết ngay khi file trên
  đĩa tiếp quản.
- **Video ghi "chưa tải" mà vẫn xem được.** Đó chính là ý đồ: bản lưu là để dành
  cho lần sau.

---

## Trang chủ

Được xếp hạng chứ không theo thứ tự thời gian, và mọi phần của thứ hạng đều giải
thích được.

**Cài đặt → Trang chủ** chia phần lớn trang cho ba nhóm:

| Nhóm | Nghĩa là |
|---|---|
| Kênh bạn theo dõi | đã đăng ký |
| Thêm thứ bạn hay xem | chưa đăng ký, về chủ đề bạn hay quay lại |
| Thứ gì đó mới | ngoài những chủ đề quen thuộc |

Phần còn lại là cố định: video bạn đang xem dở, video đã xem xong mà có thể muốn
xem lại, và video mới của kênh bạn theo dõi. Ba cái đó **không phải sở thích**,
nên không phải thứ để bạn chia.

**Để một nhóm về 0 là nó biến mất, không phải bị đẩy xuống cuối.** Những video ấy
rời khỏi trang chủ hẳn. Ô tìm kiếm và trang kênh vẫn tìm ra chúng.

**Trang chủ có thể hết bài**, và khi hết nó nói rõ, kèm tên cài đặt đã quyết định
điều đó. Trang chủ trống ở đây là chuyện bình thường chứ không phải hỏng.

**Các chip.** *Tất cả* đứng đầu, rồi *Trực tiếp* khi có kênh bạn theo dõi đang
phát, rồi tới các chủ đề thật sự có video.

---

## Các màn hình

| Màn hình | Để làm gì |
|---|---|
| **Trang chủ** | trang được xếp hạng |
| **Kênh đăng ký** | kênh bạn theo dõi; bấm vào để mở trang kênh |
| **Đã xem** | những gì đã xem, và xem tới đâu |
| **Đã lưu** | video bạn giữ lại — được ghim, không bị quét xoá |
| **Xem sau** / **Danh sách phát** | bản sao **chỉ đọc** từ tài khoản YouTube |
| **Ổ đĩa** | dung lượng đang dùng, cái nào sắp bị xoá, thư viện nằm đâu |
| **Hoạt động** | hàng chờ tải và lịch sử quét |
| **Cài đặt** | trang chủ, dịch, lồng tiếng, tài khoản, nâng cao |

**Xem sau và Danh sách phát không sửa được ở đây.** Chúng được lấy lại từ tài
khoản mỗi lần quét, nên sửa ở đây thì lần quét sau sẽ ghi đè.

**Lưu và Xem sau là hai ý định khác nhau.** Xem sau là ghi chú "cái tiếp theo sẽ
xem", tự hết khi đã xem. Lưu là giữ **file** trên đĩa khỏi bị quét xoá, và không
bao giờ tự hết.

---

## Ổ đĩa và tự xoá

Có một hạn mức. Vượt ngưỡng trên, **file** của video lâu chưa xem nhất và không
được giữ sẽ bị xoá — thông tin, ảnh thu nhỏ và lịch sử xem thì vẫn còn, và thẻ
video ghi *Đã xoá khỏi đĩa — bấm để tải lại*.

Lưu một video là miễn cho nó. Bất kỳ ai trong nhà lưu cũng miễn cho cả nhà, vì ổ
đĩa chỉ có một.

**Chỉ xem trực tiếp, không lưu** (trang Ổ đĩa) làm cho việc bấm phát không còn
xếp hàng tải nữa. Phụ đề vẫn về, file đã có vẫn xem được, nút Thử lại vẫn chạy,
và việc quét dọn vẫn tiếp tục — người ta rất có thể bật cái này **vì** ổ đã đầy.

---

## Nhiều người dùng

Thêm hồ sơ từ menu avatar. Kênh đăng ký, lịch sử, gợi ý, lượt thích, Xem sau và
danh sách phát là của riêng từng người; kho video là của cả nhà.

Đây là **tiện lợi, không phải bảo mật**. Bất cứ thứ gì trong mạng LAN đều có thể
tự nhận là bất kỳ ai bằng cách đặt một header, và link media thì không được bảo
vệ. Thứ duy nhất thật sự được bảo vệ là file cookie, bằng quyền của file.

Xoá hồ sơ chỉ xoá phần của người đó, không hơn — video và kênh vẫn còn, vì chúng
là của mọi người. Hộp xác nhận hiện số liệu thật, lấy từ chính câu truy vấn sẽ
thực hiện việc xoá.

---

## Ngôn ngữ

Menu avatar chuyển giữa **English** và **Tiếng Việt**. Đổi là ăn ngay, nhớ theo
từng máy, và mỗi ngôn ngữ được viết bằng chính nó — nên bấm nhầm vẫn quay lại
được.

---

## Khi có gì đó sai

**http://localhost:8184** là log của mọi service trên một trang, có tail trực
tiếp và bộ lọc chỉ hiện lỗi. Nó là tiến trình riêng có chủ ý: một trang xem log
nằm trong gateway sẽ chết cùng thứ mà nó sinh ra để giải thích.

`GET /api/feed/explain` trả về điểm của từng video theo từng thành phần, vị trí
của nó, và với video bị loại thì luật nào đã loại. Chỉnh trang chủ bằng cái đó
chứ đừng chỉnh bằng mắt.
