package com.jarvis.mobile

import android.content.Context
import android.util.Base64
import android.webkit.JavascriptInterface
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.ConcurrentHashMap

data class PendingDownload(
    val file: File,
    val filename: String,
    val mimeType: String,
)

class WebDownloadBridge(
    context: Context,
    private val onReady: (PendingDownload) -> Unit,
    private val onFailure: () -> Unit,
) {
    private data class ActiveDownload(
        val file: File,
        val filename: String,
        val mimeType: String,
        val stream: FileOutputStream,
    )

    private val downloadDirectory = File(context.cacheDir, "web-downloads").apply {
        mkdirs()
        listFiles { file -> file.extension == "part" }?.forEach(File::delete)
    }
    private val active = ConcurrentHashMap<String, ActiveDownload>()

    @Synchronized
    @JavascriptInterface
    fun start(id: String, filename: String, mimeType: String) {
        if (!VALID_ID.matches(id) || active.containsKey(id)) return
        try {
            val safeName = sanitizeFilename(filename)
            val file = File(downloadDirectory, "$id.part")
            active[id] = ActiveDownload(
                file = file,
                filename = safeName,
                mimeType = mimeType.ifBlank { "application/octet-stream" },
                stream = FileOutputStream(file),
            )
        } catch (_: Exception) {
            onFailure()
        }
    }

    @Synchronized
    @JavascriptInterface
    fun append(id: String, base64Chunk: String) {
        val download = active[id] ?: return
        try {
            download.stream.write(Base64.decode(base64Chunk, Base64.DEFAULT))
        } catch (_: Exception) {
            abort(id)
            onFailure()
        }
    }

    @Synchronized
    @JavascriptInterface
    fun finish(id: String) {
        val download = active.remove(id) ?: return
        try {
            download.stream.close()
            onReady(PendingDownload(download.file, download.filename, download.mimeType))
        } catch (_: Exception) {
            download.file.delete()
            onFailure()
        }
    }

    @Synchronized
    @JavascriptInterface
    fun abort(id: String) {
        val download = active.remove(id) ?: return
        runCatching { download.stream.close() }
        download.file.delete()
    }

    @Synchronized
    @JavascriptInterface
    fun fail(id: String) {
        abort(id)
        onFailure()
    }

    fun clear() {
        active.keys.toList().forEach(::abort)
    }

    private fun sanitizeFilename(value: String): String {
        val clean = value.substringAfterLast('/').substringAfterLast('\\')
            .replace(Regex("[\\u0000-\\u001f]"), "_")
            .take(160)
        return clean.ifBlank { "download" }
    }

    companion object {
        private val VALID_ID = Regex("[a-zA-Z0-9-]{1,64}")
    }
}
