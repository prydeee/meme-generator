# Fix Report — Meme Portal

## Pengantar

Laporan ini merangkum penyelesaian TKT-101 sampai TKT-109 pada take-home assignment Meme Portal. Aplikasi diuji di lingkungan lokal dengan PostgreSQL, API Go, frontend Next.js, dan mock payment gateway yang tersedia di repository. Verifikasi menggabungkan unit test, integration test yang tersedia, pemeriksaan source, observasi browser yang telah dilakukan, dan pemeriksaan regression. Klaim manual/browser dibedakan dari test yang dapat dijalankan ulang dari repository.

## Ringkasan

| Tiket | Masalah | Hasil |
| --- | --- | --- |
| TKT-101 | Subscription aktif tetap dapat dipakai setelah `ends_at` lewat | Akses kini bergantung pada status dan masa berlaku |
| TKT-102 | Status subscription dapat stale atau masuk public cache | Response dibuat private dan tidak di-cache |
| TKT-103 | Logout tidak menghapus cookie yang diterbitkan | Cookie access dan refresh terhapus pada path yang benar |
| TKT-104 | Status paket dan quota tidak langsung berubah | Query di-refetch dan di-invalidasi setelah perubahan |
| TKT-105 | Checkout ganda saat retry atau request paralel | Satu intent menghasilkan satu payment |
| TKT-106 | Pembayaran tidak memberi feedback | State proses, sukses, error, dan retry tersedia |
| TKT-107 | Meme dapat diakses lintas user | Semua operasi meme dibatasi ownership |
| TKT-108 | Upload non-gambar dan file besar dapat masuk storage | Validasi byte, metadata, ukuran, dan cleanup diterapkan |
| TKT-109 | Request paralel dapat melewati quota | Quota check dan insert berjalan atomik |

## TKT-101 — Subscription expiration

### Masalah
Subscription dengan `status = 'active'` masih dapat digunakan setelah masa berlakunya berakhir.

### Akar masalah
`ActiveByUser` hanya memeriksa status dan tidak membandingkan `ends_at` dengan waktu sekarang.

### Perbaikan
Query subscription aktif sekarang mensyaratkan `status = 'active' AND ends_at > now()`. Semua gate pembuatan meme yang menggunakan subscription aktif ikut menggunakan pemeriksaan ini.

### Verifikasi
Pemeriksaan source memastikan gate runtime memakai `ends_at > now()`. Tidak ada automated test expiration yang tersimpan di repository ini. Pengujian manual/API sebelumnya dilaporkan membandingkan subscription aktif dan kedaluwarsa, tetapi output HTTP-nya tidak tersimpan sebagai evidence yang dapat dijalankan ulang dari repository.

## TKT-102 — Cache dan subscription status

### Masalah
Status paket dan quota dapat menampilkan data lama, sementara response per-user berisiko tersimpan pada shared cache.

### Akar masalah
Endpoint status sebelumnya mengirim cache publik dan query frontend belum memasukkan identitas user ke cache key.

### Perbaikan
Endpoint status kini mengirim `Cache-Control: no-store, private`. Query frontend menggunakan user ID sebagai bagian dari key dan hanya aktif setelah identitas authenticated user tersedia.

### Verifikasi
Pemeriksaan source memastikan endpoint status memakai `Cache-Control: private, no-store` dan query frontend menggunakan user-scoped key. Tidak ada automated cache-isolation test yang tersimpan. Smoke test HTTP dan alur session sebelumnya dilaporkan dilakukan secara manual, tetapi outputnya tidak tersimpan sebagai evidence repository.

## TKT-103 — Logout cookie

### Masalah
Proses logout sebelumnya tidak selalu menghapus cookie session karena path cookie yang digunakan saat penghapusan berbeda dari path saat cookie diterbitkan. Kondisi ini dapat menyebabkan browser masih mengirimkan cookie lama setelah logout.

### Akar masalah
Cookie diterbitkan dengan `Path: /`, sedangkan cookie penghapusan sebelumnya menggunakan path `/api/v1/auth` sehingga browser memperlakukannya sebagai cookie berbeda.

### Perbaikan
Helper logout kini menghapus access token dan refresh token dengan `Path: /` dan `MaxAge: -1`. Frontend juga membersihkan query cache dan checkout intent sebelum navigasi.

### Verifikasi
`TestClearAuthCookiesUsesAuthCookiePath` memastikan kedua cookie menggunakan path dan max-age yang benar. Test ini PASS. Login/logout dan redirect loop juga telah diuji kembali pada browser tanpa reproduksi redirect loop; hasil browser tersebut tidak disimpan sebagai automated integration test.

## TKT-104 — Subscription refresh

### Masalah
Setelah pembayaran berhasil, paket dan quota pada Pricing serta navbar masih menampilkan data lama sampai halaman dimuat ulang.

### Akar masalah
`useSubscriptionStatus` memakai `staleTime: Infinity` dan perubahan setelah confirm payment atau complete meme belum selalu meng-invalidasi query.

### Perbaikan
Query subscription kini memakai `staleTime: 0`, `refetchOnMount: "always"`, query key user-scoped, dan cache-buster request. Query di-invalidasi setelah confirm payment dan melalui `useCompleteMeme` setelah meme selesai.

### Verifikasi
Mock checkout dan confirm payment telah diamati menghasilkan status pembayaran sukses dan status subscription aktif pada pengujian manual/browser sebelumnya. Screenshot representatif tersedia: [Pricing setelah pembayaran](docs/evidence/TKT-104/subscription-pricing.png). Tidak ada automated test repository yang secara khusus memverifikasi refresh navbar dan quota tanpa hard reload.

## TKT-105 — Checkout idempotency

### Masalah
Double-click atau retry dapat membuat checkout pending ganda untuk pembelian yang sama.

### Akar masalah
Frontend membuat key baru pada setiap mutation. Di backend, pre-check dan insert terpisah sehingga request paralel dapat melewati pemeriksaan bersamaan.

### Perbaikan
Backend memakai transaction-scoped advisory lock berdasarkan user ID, mencari pending checkout di dalam transaksi, dan menyimpan idempotency key pada payment. Frontend mempertahankan checkout intent berdasarkan user dan plan, termasuk saat retry, serta membersihkannya saat logout atau user berubah.

### Verifikasi
`TestCheckoutSameKeyConcurrentHTTP` melepas 8 request HTTP bersamaan terhadap API lokal: seluruhnya berhasil, tetapi menghasilkan satu payment reference, satu idempotency key, dan satu payment di database. Test ini PASS dengan PostgreSQL lokal. Test tersebut tidak mencakup perbandingan user berbeda.

## TKT-106 — Payment feedback

### Masalah
Pembayaran gagal tanpa pesan, sementara tombol tidak menunjukkan proses atau menyediakan feedback yang jelas.

### Akar masalah
Error ditangkap dengan `catch` kosong dan tombol tidak menggunakan state pending mutation.

### Perbaikan
UI menampilkan `Memproses…` saat mutation berjalan, menonaktifkan tombol, menampilkan error dengan `role="alert"`, mempertahankan retry setelah gagal, serta menampilkan `Lunas` setelah berhasil.

### Verifikasi
Pada browser, pembayaran mock berhasil menampilkan pesan sukses, mengubah tombol menjadi `Lunas`, menonaktifkannya, lalu melakukan navigasi. Reference invalid menampilkan alert `pembayaran gagal`, tidak melakukan navigasi sukses, dan tombol tetap tersedia untuk retry. Tidak ada automated frontend test untuk state pembayaran tersebut; bukti yang tersedia adalah observasi browser.

## TKT-107 — Meme ownership

### Masalah
Mengganti ID meme pada URL atau request dapat membuka operasi terhadap meme user lain.

### Akar masalah
Query meme sebelumnya hanya memfilter ID dan tidak memasukkan `user_id` dari authenticated context.

### Perbaikan
Get, result upload, complete, dan delete kini menggunakan ownership-scoped query dengan pasangan `id` dan `user_id`. Ownership diperiksa sebelum akses object storage maupun perubahan data.

### Verifikasi
Repository tidak memiliki automated integration test yang membuat dua user lalu menguji seluruh operasi lintas kepemilikan. Pemeriksaan source menunjukkan query ownership-scoped pada operasi tersebut. Karena output API/browser dua user tidak tersimpan di evidence repository, klaim HTTP 404 lintas user tidak dinyatakan sebagai hasil automated integration test.

## TKT-108 — Upload validation dan cleanup

### Masalah
Presigned upload dapat menerima file non-gambar, metadata yang menyesatkan, dan file berukuran terlalu besar.

### Akar masalah
Validasi sebelumnya terlalu bergantung pada informasi dari client dan tidak memeriksa byte aktual sebelum object digunakan.

### Perbaikan
Service membatasi JPG, JPEG, PNG, GIF, dan WebP hingga 10 MB; memeriksa signature, decoder, dimensi, pixel count, metadata, dan kecocokan format dengan ekstensi. Object invalid dihapus, record meme dibersihkan bila presign gagal, dan ownership diperiksa sebelum akses result object.

### Verifikasi
Unit test memeriksa ekstensi terlarang, ZIP bernama gambar, WebP valid/invalid, mismatch metadata dan byte, ukuran, dimensi, serta pixel count. Storage mock test memastikan `HeadObject`, `GetObject`, validasi byte, dan delete failure memicu cleanup. `TestPrepareUploadPresignFailureCleansFixtureRecord` memastikan record database kembali ke jumlah sebelum test saat presign gagal.

## TKT-109 — Quota concurrency

### Masalah
Request paralel dari beberapa tab dapat melewati batas quota karena semuanya membaca sisa quota yang sama.

### Akar masalah
Quota check dan insert sebelumnya merupakan operasi terpisah dengan pola check-then-insert.

### Perbaikan
`CreateWithQuota` menjalankan subscription check, perhitungan quota, dan insert di dalam satu transaksi PostgreSQL dengan transaction-scoped advisory lock berdasarkan user ID. Request yang melebihi quota ditolak sebelum insert.

### Verifikasi
`TestCreateWithQuotaConcurrentPostgres` dijalankan terhadap PostgreSQL lokal. Enam request bersamaan menghasilkan dua request berhasil dan empat ditolak sesuai quota; tidak ada overshoot dan fixture dibersihkan. Run yang tercatat dalam audit ini PASS; laporan tidak mengklaim pengulangan tiga kali karena output tiga run tidak tersimpan.

## Regression verification

Pemeriksaan regression lokal menghasilkan:

- `bun run check`: PASS setelah `bun run build` membuat `.next/types`; eksekusi sebelum build gagal karena generated Next type tersebut belum tersedia
- `bun run lint`: PASS, dengan warning existing bahwa `formatIdr` tidak digunakan
- `bun run build`: PASS
- `go test ./...`: PASS
- `go build -o bin/api ./cmd/server`: PASS
- `git diff --check`: PASS