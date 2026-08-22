package com.jarvis.mobile

import android.graphics.Bitmap
import android.net.Uri
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient

class GatewayWebViewClient(
    private val serverUrl: () -> String?,
    private val openExternal: (Uri) -> Unit,
    private val onPageStarted: () -> Unit,
    private val onPageFinished: (WebView) -> Unit,
    private val onMainFrameError: (String) -> Unit,
) : WebViewClient() {

    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        val uri = request.url
        if (uri.scheme in setOf("http", "https") && isServerOrigin(uri)) return false
        openExternal(uri)
        return true
    }

    override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
        onPageStarted()
    }

    override fun onPageFinished(view: WebView, url: String) {
        onPageFinished(view)
    }

    override fun onReceivedError(
        view: WebView,
        request: WebResourceRequest,
        error: WebResourceError,
    ) {
        if (request.isForMainFrame) onMainFrameError(error.description.toString())
    }

    override fun onReceivedHttpError(
        view: WebView,
        request: WebResourceRequest,
        errorResponse: WebResourceResponse,
    ) {
        if (request.isForMainFrame && errorResponse.statusCode >= 400) {
            onMainFrameError("HTTP ${errorResponse.statusCode}")
        }
    }

    private fun isServerOrigin(uri: Uri): Boolean {
        val configured = serverUrl()?.let(Uri::parse) ?: return false
        return configured.scheme.equals(uri.scheme, ignoreCase = true) &&
            configured.host.equals(uri.host, ignoreCase = true) &&
            effectivePort(configured) == effectivePort(uri)
    }

    private fun effectivePort(uri: Uri): Int = when {
        uri.port >= 0 -> uri.port
        uri.scheme.equals("https", ignoreCase = true) -> 443
        else -> 80
    }
}
