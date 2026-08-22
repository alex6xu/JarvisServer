package com.jarvis.mobile

import android.content.Context
import android.net.Uri

class ServerSettings(context: Context) {
    private val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)

    var serverUrl: String?
        get() = preferences.getString(KEY_SERVER_URL, null)
        set(value) {
            preferences.edit().putString(KEY_SERVER_URL, value).apply()
        }

    companion object {
        private const val PREFERENCES = "jarvis_mobile"
        private const val KEY_SERVER_URL = "server_url"

        fun normalize(rawValue: String): String? {
            val trimmed = rawValue.trim()
            if (trimmed.isEmpty() || trimmed.any(Char::isWhitespace)) return null
            val candidate = if (trimmed.contains("://")) trimmed else "https://$trimmed"
            val uri = Uri.parse(candidate)
            val scheme = uri.scheme?.lowercase()
            if (scheme !in setOf("http", "https") || uri.host.isNullOrBlank()) return null
            if (!uri.userInfo.isNullOrBlank()) return null

            val cleanPath = uri.path.orEmpty().trimEnd('/').let { if (it.isEmpty()) "" else it }
            return uri.buildUpon()
                .encodedPath(cleanPath)
                .clearQuery()
                .fragment(null)
                .build()
                .toString()
                .trimEnd('/')
        }
    }
}
