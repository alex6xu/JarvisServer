package com.jarvis.mobile

import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.Intent
import android.graphics.Color
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.Uri
import android.os.Bundle
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.DownloadListener
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.widget.EditText
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import kotlin.concurrent.thread

class MainActivity : Activity() {
    private lateinit var webView: WebView
    private lateinit var progress: ProgressBar
    private lateinit var errorPanel: View
    private lateinit var errorTitle: TextView
    private lateinit var errorDetail: TextView
    private lateinit var serverSettings: ServerSettings
    private lateinit var downloadBridge: WebDownloadBridge
    private lateinit var connectivityManager: ConnectivityManager

    private var fileChooserCallback: ValueCallback<Array<Uri>>? = null
    private var pendingDownload: PendingDownload? = null
    private var networkCallbackRegistered = false
    private var pageFailed = false

    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onLost(network: Network) {
            if (!hasInternetConnection()) runOnUiThread { showOffline() }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        serverSettings = ServerSettings(this)
        connectivityManager = getSystemService(ConnectivityManager::class.java)
        bindViews()
        configureWebView()

        findViewById<View>(R.id.refresh_button).setOnClickListener { loadServer(force = true) }
        findViewById<View>(R.id.settings_button).setOnClickListener { showServerDialog(firstRun = false) }
        findViewById<View>(R.id.retry_button).setOnClickListener { loadServer(force = true) }
        findViewById<View>(R.id.change_server_button).setOnClickListener { showServerDialog(firstRun = false) }

        val restored = savedInstanceState != null && webView.restoreState(savedInstanceState) != null
        if (!restored) {
            if (serverSettings.serverUrl == null) showServerDialog(firstRun = true) else loadServer()
        }
    }

    private fun bindViews() {
        webView = findViewById(R.id.web_view)
        progress = findViewById(R.id.progress)
        errorPanel = findViewById(R.id.error_panel)
        errorTitle = findViewById(R.id.error_title)
        errorDetail = findViewById(R.id.error_detail)
    }

    @Suppress("SetJavaScriptEnabled")
    private fun configureWebView() {
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG)
        webView.setBackgroundColor(Color.rgb(247, 247, 248))
        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            databaseEnabled = true
            allowFileAccess = false
            allowContentAccess = true
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            cacheMode = WebSettings.LOAD_DEFAULT
            mediaPlaybackRequiresUserGesture = true
            setSupportMultipleWindows(true)
            javaScriptCanOpenWindowsAutomatically = true
            userAgentString = "$userAgentString JarvisAndroid/${BuildConfig.VERSION_NAME}"
        }
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setAcceptThirdPartyCookies(webView, true)
        }

        downloadBridge = WebDownloadBridge(
            context = this,
            onReady = { runOnUiThread { prepareSave(it) } },
            onFailure = { runOnUiThread { toast(R.string.download_failed) } },
        )
        webView.addJavascriptInterface(downloadBridge, "JarvisAndroid")
        webView.webViewClient = GatewayWebViewClient(
            serverUrl = { serverSettings.serverUrl },
            openExternal = ::openExternal,
            onPageStarted = {
                pageFailed = false
                errorPanel.visibility = View.GONE
                progress.visibility = View.VISIBLE
            },
            onPageFinished = { view ->
                progress.visibility = View.GONE
                if (!pageFailed) errorPanel.visibility = View.GONE
                installBlobDownloadHandler(view)
            },
            onMainFrameError = ::showPageError,
        )
        webView.webChromeClient = createWebChromeClient()
        webView.setDownloadListener(DownloadListener { url, _, _, _, _ ->
            openExternal(Uri.parse(url))
        })
    }

    private fun createWebChromeClient() = object : WebChromeClient() {
        override fun onProgressChanged(view: WebView, newProgress: Int) {
            progress.progress = newProgress
            progress.visibility = if (newProgress in 0..99) View.VISIBLE else View.GONE
        }

        override fun onShowFileChooser(
            webView: WebView,
            filePathCallback: ValueCallback<Array<Uri>>,
            fileChooserParams: FileChooserParams,
        ): Boolean {
            fileChooserCallback?.onReceiveValue(null)
            fileChooserCallback = filePathCallback
            val acceptTypes = fileChooserParams.acceptTypes
                .flatMap { it.split(',') }
                .map(String::trim)
                .filter(String::isNotEmpty)
                .toTypedArray()
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                addCategory(Intent.CATEGORY_OPENABLE)
                type = if (acceptTypes.size == 1) acceptTypes[0] else "*/*"
                putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
                if (acceptTypes.size > 1) putExtra(Intent.EXTRA_MIME_TYPES, acceptTypes)
            }
            return try {
                startActivityForResult(Intent.createChooser(intent, getString(R.string.choose_files)), REQUEST_FILES)
                true
            } catch (_: ActivityNotFoundException) {
                fileChooserCallback = null
                false
            }
        }

        override fun onCreateWindow(
            view: WebView,
            isDialog: Boolean,
            isUserGesture: Boolean,
            resultMsg: android.os.Message,
        ): Boolean {
            val temporary = WebView(this@MainActivity)
            var opened = false
            fun openOnce(uri: Uri) {
                if (opened) return
                opened = true
                openExternal(uri)
            }
            temporary.webViewClient = object : android.webkit.WebViewClient() {
                override fun shouldOverrideUrlLoading(v: WebView, request: android.webkit.WebResourceRequest): Boolean {
                    openOnce(request.url)
                    v.destroy()
                    return true
                }

                override fun onPageStarted(v: WebView, url: String, favicon: android.graphics.Bitmap?) {
                    openOnce(Uri.parse(url))
                    v.stopLoading()
                    v.destroy()
                }
            }
            (resultMsg.obj as WebView.WebViewTransport).webView = temporary
            resultMsg.sendToTarget()
            return true
        }
    }

    private fun loadServer(force: Boolean = false) {
        val url = serverSettings.serverUrl ?: return showServerDialog(firstRun = true)
        if (!hasInternetConnection()) {
            showOffline()
            return
        }
        errorPanel.visibility = View.GONE
        if (force && webView.url != null) webView.reload() else webView.loadUrl(url)
    }

    private fun showServerDialog(firstRun: Boolean) {
        val input = EditText(this).apply {
            setSingleLine(true)
            hint = getString(R.string.server_address_hint)
            setText(serverSettings.serverUrl.orEmpty())
            selectAll()
            setPadding(0, 20, 0, 8)
        }
        val container = android.widget.LinearLayout(this).apply {
            orientation = android.widget.LinearLayout.VERTICAL
            setPadding(48, 0, 48, 0)
            addView(input, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
            addView(TextView(this@MainActivity).apply {
                text = getString(R.string.server_address_help)
                textSize = 12f
                setTextColor(Color.DKGRAY)
            })
        }
        val dialog = AlertDialog.Builder(this)
            .setTitle(R.string.server_address)
            .setView(container)
            .setCancelable(!firstRun)
            .setPositiveButton(R.string.save_and_open, null)
            .apply { if (!firstRun) setNegativeButton(R.string.cancel, null) }
            .create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val normalized = ServerSettings.normalize(input.text.toString())
                val error = when {
                    normalized == null -> getString(R.string.invalid_server_address)
                    !BuildConfig.DEBUG && Uri.parse(normalized).scheme != "https" -> getString(R.string.https_required)
                    else -> null
                }
                if (error != null) {
                    input.error = error
                    return@setOnClickListener
                }
                val changed = normalized != serverSettings.serverUrl
                serverSettings.serverUrl = normalized
                if (changed) webView.clearHistory()
                dialog.dismiss()
                loadServer()
            }
        }
        dialog.show()
    }

    private fun showPageError(detail: String) {
        pageFailed = true
        progress.visibility = View.GONE
        errorTitle.setText(R.string.page_unavailable)
        errorDetail.text = detail.ifBlank { getString(R.string.check_network) }
        errorPanel.visibility = View.VISIBLE
    }

    private fun showOffline() {
        pageFailed = true
        progress.visibility = View.GONE
        errorTitle.setText(R.string.offline)
        errorDetail.setText(R.string.check_network)
        errorPanel.visibility = View.VISIBLE
    }

    private fun hasInternetConnection(): Boolean {
        val network = connectivityManager.activeNetwork ?: return false
        val capabilities = connectivityManager.getNetworkCapabilities(network) ?: return false
        return capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
    }

    private fun openExternal(uri: Uri) {
        if (uri.scheme == "blob") {
            toast(R.string.download_failed)
            return
        }
        try {
            startActivity(Intent(Intent.ACTION_VIEW, uri))
        } catch (_: ActivityNotFoundException) {
            toast(R.string.opening_external_failed)
        }
    }

    private fun installBlobDownloadHandler(view: WebView) {
        view.evaluateJavascript(BLOB_DOWNLOAD_SCRIPT, null)
    }

    private fun prepareSave(download: PendingDownload) {
        pendingDownload?.file?.delete()
        pendingDownload = download
        val intent = Intent(Intent.ACTION_CREATE_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = download.mimeType
            putExtra(Intent.EXTRA_TITLE, download.filename)
        }
        try {
            startActivityForResult(intent, REQUEST_SAVE_DOWNLOAD)
            toast(R.string.download_ready)
        } catch (_: ActivityNotFoundException) {
            download.file.delete()
            pendingDownload = null
            toast(R.string.download_failed)
        }
    }

    @Deprecated("Uses the platform Activity API to keep this shell dependency-free")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        when (requestCode) {
            REQUEST_FILES -> {
                val callback = fileChooserCallback ?: return
                fileChooserCallback = null
                callback.onReceiveValue(if (resultCode == RESULT_OK) extractUris(data) else null)
            }
            REQUEST_SAVE_DOWNLOAD -> {
                val download = pendingDownload ?: return
                pendingDownload = null
                val destination = data?.data
                if (resultCode != RESULT_OK || destination == null) {
                    download.file.delete()
                    return
                }
                thread(name = "jarvis-save-download") {
                    val saved = runCatching {
                        contentResolver.openOutputStream(destination, "w")!!.use { output ->
                            download.file.inputStream().use { input -> input.copyTo(output) }
                        }
                    }.isSuccess
                    download.file.delete()
                    runOnUiThread { toast(if (saved) R.string.download_saved else R.string.download_failed) }
                }
            }
        }
    }

    private fun extractUris(data: Intent?): Array<Uri>? {
        if (data == null) return null
        val uris = mutableListOf<Uri>()
        data.clipData?.let { clip ->
            for (index in 0 until clip.itemCount) uris += clip.getItemAt(index).uri
        }
        data.data?.let { if (it !in uris) uris += it }
        return uris.takeIf { it.isNotEmpty() }?.toTypedArray()
    }

    override fun onStart() {
        super.onStart()
        if (!networkCallbackRegistered) {
            connectivityManager.registerDefaultNetworkCallback(networkCallback)
            networkCallbackRegistered = true
        }
    }

    override fun onStop() {
        if (networkCallbackRegistered) {
            connectivityManager.unregisterNetworkCallback(networkCallback)
            networkCallbackRegistered = false
        }
        super.onStop()
    }

    override fun onPause() {
        webView.onPause()
        CookieManager.getInstance().flush()
        super.onPause()
    }

    override fun onResume() {
        super.onResume()
        webView.onResume()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        webView.saveState(outState)
        super.onSaveInstanceState(outState)
    }

    @Deprecated("Uses WebView history before leaving the activity")
    override fun onBackPressed() {
        if (webView.canGoBack()) webView.goBack() else super.onBackPressed()
    }

    override fun onDestroy() {
        fileChooserCallback?.onReceiveValue(null)
        fileChooserCallback = null
        pendingDownload?.file?.delete()
        downloadBridge.clear()
        webView.apply {
            loadUrl("about:blank")
            stopLoading()
            removeJavascriptInterface("JarvisAndroid")
            destroy()
        }
        super.onDestroy()
    }

    private fun toast(message: Int) = Toast.makeText(this, message, Toast.LENGTH_SHORT).show()

    companion object {
        private const val REQUEST_FILES = 1001
        private const val REQUEST_SAVE_DOWNLOAD = 1002

        private val BLOB_DOWNLOAD_SCRIPT = """
            (() => {
              if (window.__jarvisAndroidDownloadInstalled) return;
              window.__jarvisAndroidDownloadInstalled = true;
              const originalClick = HTMLAnchorElement.prototype.click;
              HTMLAnchorElement.prototype.click = function() {
                const href = this.href || '';
                if (!href.startsWith('blob:') || !this.download) {
                  return originalClick.call(this);
                }
                const filename = this.download || 'download';
                const id = Date.now().toString(36) + '-' + Math.random().toString(36).slice(2);
                (async () => {
                  try {
                    const response = await fetch(href);
                    if (!response.ok) throw new Error('blob read failed');
                    JarvisAndroid.start(id, filename, response.headers.get('content-type') || 'application/octet-stream');
                    const reader = response.body.getReader();
                    while (true) {
                      const part = await reader.read();
                      if (part.done) break;
                      const bytes = part.value;
                      let binary = '';
                      for (let offset = 0; offset < bytes.length; offset += 32768) {
                        binary += String.fromCharCode.apply(null, bytes.subarray(offset, offset + 32768));
                      }
                      JarvisAndroid.append(id, btoa(binary));
                    }
                    JarvisAndroid.finish(id);
                  } catch (_) {
                    JarvisAndroid.fail(id);
                  }
                })();
              };
            })();
        """.trimIndent()
    }
}
