package persona.feature.settings

import android.app.ActivityManager
import android.app.ApplicationExitInfo
import android.content.Context
import android.content.res.Resources
import android.os.Build
import android.os.PowerManager
import androidx.annotation.StringRes
import java.time.Instant

/** Only the numeric fields approved for the user-copied diagnostic report cross this boundary. */
internal data class BackgroundExit(
    val timestamp: Long,
    val reason: Int,
    val status: Int,
    val importance: Int,
    val pssKb: Long,
    val rssKb: Long,
) {
    fun text(resources: Resources): String =
        "timestamp=$timestamp (${Instant.ofEpochMilli(timestamp)})\n" +
            "reason=$reason (${resources.getString(backgroundReason(reason))})\n" +
            "status=$status importance=$importance PSS=$pssKb kB RSS=$rssKb kB"
}

internal data class BackgroundDiagnostics(
    val appVersion: String?,
    val androidVersion: String,
    val manufacturer: String,
    val model: String,
    val batteryExempt: Boolean?,
    val backgroundRestricted: Boolean?,
    val exits: List<BackgroundExit>?,
) {
    fun rows(resources: Resources): List<Pair<String, String>> {
        val unavailable = resources.getString(R.string.settings_background_unavailable)
        return listOf(
            resources.getString(R.string.settings_background_app_version) to (appVersion ?: unavailable),
            resources.getString(R.string.settings_background_android) to androidVersion,
            resources.getString(R.string.settings_background_manufacturer) to manufacturer,
            resources.getString(R.string.settings_background_model) to model,
            resources.getString(R.string.settings_background_battery) to when (batteryExempt) {
                true -> resources.getString(R.string.settings_background_exempt)
                false -> resources.getString(R.string.settings_background_not_exempt)
                null -> unavailable
            },
            resources.getString(R.string.settings_background_restriction) to when (backgroundRestricted) {
                true -> resources.getString(R.string.settings_background_restricted)
                false -> resources.getString(R.string.settings_background_not_restricted)
                null -> unavailable
            },
            resources.getString(R.string.settings_background_history) to when {
                exits == null -> unavailable
                exits.isEmpty() -> resources.getString(R.string.settings_background_no_history)
                else -> resources.getQuantityString(R.plurals.settings_background_history_count, exits.size, exits.size)
            },
        ) + exits.orEmpty().mapIndexed { index, exit ->
            resources.getString(R.string.settings_background_exit, index + 1) to exit.text(resources)
        }
    }

    fun copyText(resources: Resources): String = buildString {
        appendLine(resources.getString(R.string.settings_background_title))
        rows(resources).forEach { (label, value) -> appendLine("$label: $value") }
        append(resources.getString(R.string.settings_background_note))
    }

    companion object {
        fun read(context: Context): BackgroundDiagnostics {
            val app = context.applicationContext
            val manager = app.getSystemService(ActivityManager::class.java)
            @Suppress("DEPRECATION")
            val version = runCatching { app.packageManager.getPackageInfo(app.packageName, 0) }.getOrNull()
            return BackgroundDiagnostics(
                appVersion = version?.let { "${it.versionName.orEmpty()} (${it.longVersionCode})" },
                androidVersion = "${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})",
                manufacturer = Build.MANUFACTURER,
                model = Build.MODEL,
                batteryExempt = runCatching { app.getSystemService(PowerManager::class.java)
                    .isIgnoringBatteryOptimizations(app.packageName) }.getOrNull(),
                backgroundRestricted = runCatching { manager.isBackgroundRestricted }.getOrNull(),
                exits = readBackgroundExits(app.packageName) { manager.getHistoricalProcessExitReasons(app.packageName, 0, 16) },
            )
        }
    }
}

internal fun readBackgroundExits(processName: String, read: () -> List<ApplicationExitInfo>): List<BackgroundExit>? = try {
    read().filter { it.processName == processName }.sortedByDescending { it.timestamp }.take(3).map {
        BackgroundExit(it.timestamp, it.reason, it.status, it.importance, it.pss, it.rss)
    }
} catch (_: Exception) { null }

@StringRes
private fun backgroundReason(reason: Int): Int = when (reason) {
    ApplicationExitInfo.REASON_EXIT_SELF -> R.string.settings_background_reason_self
    ApplicationExitInfo.REASON_SIGNALED -> R.string.settings_background_reason_signal
    ApplicationExitInfo.REASON_LOW_MEMORY -> R.string.settings_background_reason_memory
    ApplicationExitInfo.REASON_CRASH -> R.string.settings_background_reason_crash
    ApplicationExitInfo.REASON_CRASH_NATIVE -> R.string.settings_background_reason_native
    ApplicationExitInfo.REASON_ANR -> R.string.settings_background_reason_anr
    ApplicationExitInfo.REASON_INITIALIZATION_FAILURE -> R.string.settings_background_reason_initialization
    ApplicationExitInfo.REASON_PERMISSION_CHANGE -> R.string.settings_background_reason_permission
    ApplicationExitInfo.REASON_EXCESSIVE_RESOURCE_USAGE -> R.string.settings_background_reason_resource
    ApplicationExitInfo.REASON_USER_REQUESTED -> R.string.settings_background_reason_requested
    ApplicationExitInfo.REASON_USER_STOPPED -> R.string.settings_background_reason_user_stopped
    ApplicationExitInfo.REASON_DEPENDENCY_DIED -> R.string.settings_background_reason_dependency
    ApplicationExitInfo.REASON_FREEZER -> R.string.settings_background_reason_freezer
    ApplicationExitInfo.REASON_PACKAGE_STATE_CHANGE -> R.string.settings_background_reason_package_state
    ApplicationExitInfo.REASON_PACKAGE_UPDATED -> R.string.settings_background_reason_update
    ApplicationExitInfo.REASON_OTHER -> R.string.settings_background_reason_other
    else -> R.string.settings_background_reason_unknown
}
