# Deep Backend

باك أند موجّه للإنتاج مبني بلغة **Go** لتطبيق Android الخاص بكشف الوسائط وتنزيلها.

---

## لماذا Chi بدلاً من Gin؟

اخترنا **chi** لأنه:
- يعتمد على `net/http` الرسمي بالكامل — لا magic، لا reflection
- يدعم middleware composability باستخدام `chi.Router.With()`
- خفيف جداً (~5 KB binary overhead)
- يتوافق مع أي `http.Handler` standard

---

## هيكل المشروع

```
deep-backend/
├── cmd/api/main.go              ← نقطة الدخول، wire كل الطبقات
├── internal/
│   ├── config/config.go         ← تحميل متغيرات البيئة
│   ├── domain/domain.go         ← كل الـ entities والـ DTOs
│   ├── store/
│   │   ├── interfaces.go        ← واجهات Repository
│   │   └── postgres.go          ← تنفيذ pgx/v5 (متوافق مع Neon/pgbouncer)
│   ├── http/
│   │   ├── handler/handler.go   ← HTTP handlers (لا منطق أعمال هنا)
│   │   └── middleware/          ← RequestID، Logger، Recover
│   ├── service/media_service.go ← منطق الأعمال (يقف بين handler وstore)
│   ├── jobs/worker.go           ← Worker pool بـ polling آمن على قاعدة البيانات
│   ├── media/
│   │   ├── probe.go             ← مغلف ffprobe
│   │   ├── processor.go         ← مغلف ffmpeg (extract, merge, transcode)
│   │   ├── analyzer.go          ← محرّك التحليل (YouTube + Direct)
│   │   └── ytdlp.go             ← تكامل yt-dlp لـ YouTube
│   ├── storage/
│   │   ├── backend.go           ← واجهة مجردة للتخزين
│   │   └── local.go             ← تنفيذ نظام الملفات المحلي
│   └── auth/jwt.go              ← خدمة JWT (اختيارية وقابلة للفصل)
├── migrations/
│   ├── 000001_init.up.sql
│   └── 000001_init.down.sql
├── docs/openapi.yaml            ← مواصفة OpenAPI 3.1
├── .env.example
├── Dockerfile
└── docker-compose.yml
```

---

## خطوات الإعداد السريع

### 1. المتطلبات المسبقة

| الأداة | الإصدار الأدنى |
|--------|----------------|
| Go | 1.23 |
| ffmpeg + ffprobe | أي إصدار حديث |
| yt-dlp | `pip install yt-dlp` |
| Docker (اختياري) | 24+ |

### 2. إعداد متغيرات البيئة

```bash
cp .env.example .env
```

عدّل `.env` وضع الـ connection string من Neon:

```
DATABASE_URL=postgresql://neondb_owner:YOUR_PASSWORD@ep-calm-base-anna5afn-pooler.c-6.us-east-1.aws.neon.tech/neondb?sslmode=require&channel_binding=require
```

> **ملاحظة**: تأكد من تفعيل **Connection pooling** في Neon Dashboard لأن الخادم يستخدم pgbouncer.

### 3. تشغيل محلي

```powershell
# تنزيل الاعتماديات
go mod tidy

# تشغيل الخادم (الترحيلات تُطبَّق تلقائياً عند البدء)
go run ./cmd/api
```

### 4. تشغيل بـ Docker

```bash
# انسخ .env.example ثم أضف DATABASE_URL الحقيقي
cp .env.example .env

docker compose up --build
```

الخادم يعمل على `http://localhost:8080`

---

## متغيرات البيئة المطلوبة

| المتغير | مطلوب؟ | الافتراضي | الوصف |
|---------|---------|-----------|-------|
| `DATABASE_URL` | ✅ | — | Neon PostgreSQL connection string (مع SSL) |
| `PORT` | | `8080` | منفذ HTTP |
| `STORAGE_BACKEND` | | `local` | `local` أو `s3` |
| `LOCAL_STORAGE_PATH` | | `./tmp/storage` | مسار التخزين المحلي |
| `FFMPEG_PATH` | | `ffmpeg` | مسار ملف ffmpeg |
| `FFPROBE_PATH` | | `ffprobe` | مسار ملف ffprobe |
| `TEMP_DIR` | | `./tmp/jobs` | مجلد الملفات المؤقتة |
| `WORKER_COUNT` | | `4` | عدد عمال المعالجة |
| `JWT_ENABLED` | | `false` | تفعيل حماية JWT |
| `JWT_SECRET` | إذا JWT_ENABLED | — | مفتاح JWT (32+ حرف) |

---

## تدفق API

```
POST /v1/analyze          ← أرسل URL
  ↓ returns { job_id }
GET  /v1/jobs/{id}        ← polling (كل 2 ثانية)
  ↓ status: completed
GET  /v1/jobs/{id}/variants ← اختر صيغة
  ↓
POST /v1/jobs/{id}/actions/extract-audio  ← أو merge أو transcode
  ↓ returns { job_id }
GET  /v1/jobs/{id}        ← polling
  ↓ status: completed + asset
GET  /v1/assets/dl/{token} ← تنزيل الملف النهائي
```

---

## Endpoints

| Method | Path | الوصف | Rate Limit |
|--------|------|-------|-----------|
| `GET` | `/healthz` | فحص الحياة | — |
| `GET` | `/readyz` | فحص الجاهزية | — |
| `POST` | `/v1/analyze` | إرسال URL للتحليل | 20/دقيقة |
| `GET` | `/v1/jobs/{id}` | حالة الوظيفة + Asset | — |
| `GET` | `/v1/jobs/{id}/variants` | الصيغ المتاحة | — |
| `POST` | `/v1/jobs/{id}/actions/extract-audio` | استخراج صوت | — |
| `POST` | `/v1/jobs/{id}/actions/merge` | دمج فيديو+صوت | — |
| `POST` | `/v1/jobs/{id}/actions/transcode` | تحويل الصيغة | — |
| `GET` | `/v1/assets/{id}` | بيانات الـ asset | — |
| `GET` | `/v1/assets/dl/{token}` | تنزيل مباشر بتوكن | — |

---

## دمج Android

### 1. أضف الاعتماديات في `build.gradle.kts`

```kotlin
// Retrofit + OkHttp
implementation("com.squareup.retrofit2:retrofit:2.11.0")
implementation("com.squareup.retrofit2:converter-gson:2.11.0")
implementation("com.squareup.okhttp3:logging-interceptor:4.12.0")
```

### 2. أنشئ الـ client (مرة واحدة في DI)

```kotlin
val api = RetrofitClient.create(
    baseUrl = "http://10.0.2.2:8080/", // emulator → localhost
    enableLogging = BuildConfig.DEBUG
)
val repo = MediaBackendRepository(api)
val viewModel = MediaBackendViewModel(repo)
```

### 3. ربط مع BrowserScreen

```kotlin
// داخل BrowserScreen — عند YouTube أو مصدر يحتاج تحليل:
var showBackendSheet by remember { mutableStateOf(false) }

// عند اكتشاف URL صعب، اعرض خيار المعالجة:
SaveOptionsSheet(
    // ... الخيارات الحالية +
    onProcessViaServer = { showBackendSheet = true }
)

if (showBackendSheet) {
    BackendProcessSheet(
        url = currentPageUrl,
        viewModel = backendViewModel,
        onDownload = { asset ->
            // افتح رابط التنزيل في المتصفح أو Download Manager
            context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(asset.downloadUrl)))
        },
        onDismiss = { showBackendSheet = false }
    )
}
```

### الملفات الجديدة (لا تُعدّل الملفات القديمة)

| الملف | الوصف |
|-------|-------|
| `data/backend/model/BackendModels.kt` | DTOs تطابق استجابات Go |
| `data/backend/api/DeepApiService.kt` | واجهة Retrofit |
| `data/backend/api/RetrofitClient.kt` | إعداد OkHttp |
| `data/backend/repository/MediaBackendRepository.kt` | Repository مع Flow polling |
| `ui/viewmodel/MediaBackendViewModel.kt` | ViewModel مع state machine |
| `ui/screens/browser/BackendProcessSheet.kt` | Compose sheet جديد |

---

## دعم YouTube

الباك أند يدعم YouTube عبر:
1. **التعرف على الرابط**: `youtube.com/watch` أو `youtu.be/`
2. **yt-dlp**: يُشغَّل بـ `--dump-json` لجلب كل الصيغ
3. **التصنيف**: فيديو+صوت / فيديو فقط (adaptive) / صوت فقط
4. **الدمج**: `merge` action يجمع أفضل فيديو + صوت بـ FFmpeg

---

## ما يتبقى لـ Production Hardening

- [ ] **S3/R2 Storage**: تنفيذ `storage.Backend` لـ Cloudflare R2
- [ ] **HMAC Signed URLs**: توقيع روابط التنزيل بـ HMAC بدلاً من UUID البسيط
- [ ] **تفعيل JWT**: `JWT_ENABLED=true` + إدارة المستخدمين
- [ ] **Observability**: إضافة OpenTelemetry tracing
- [ ] **yt-dlp cookies**: دعم روابط YouTube المحمية بحساب (cookies.txt)
- [ ] **Webhook**: إشعار push بدلاً من polling (SSE أو WebSocket)
- [ ] **Anti-abuse**: IP blocking + queue limits لكل مستخدم
- [ ] **CDN**: إضافة Cloudflare أمام الـ assets للتنزيل السريع
