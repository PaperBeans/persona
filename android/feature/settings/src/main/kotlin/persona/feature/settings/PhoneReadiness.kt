package persona.feature.settings

import android.app.ActivityManager
import android.app.NotificationManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import java.net.Inet6Address
import java.util.concurrent.TimeUnit

data class SystemKind(val name: String, val version: String, val property: String)
internal data class SettingsTarget(val action: String? = null, val component: String? = null, val packageUri: Boolean = false)
internal data class ReadyItem(val id: String, val title: String, val detail: String, val satisfied: Boolean?, val targets: List<SettingsTarget>)
internal val appDetails = SettingsTarget(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, packageUri = true)
internal val notificationSettings = SettingsTarget(Settings.ACTION_APP_NOTIFICATION_SETTINGS)

/**
 * The phone's system, worked out once per system build (#4041). [PhoneReadiness.system] starts up to six `getprop`
 * processes one after another, and the connection asks for it while the app's process starts, with the main thread
 * waiting. Every system update changes [Build.FINGERPRINT], and with it the answer.
 */
fun detectedPhoneSystem(context: Context): SystemKind {
    val preferences = context.getSharedPreferences("phone-system", Context.MODE_PRIVATE)
    return cachedPhoneSystem(preferences.getString("system", null), Build.FINGERPRINT.orEmpty(), PhoneReadiness::system) {
        preferences.edit().putString("system", it).apply()
    }
}

/** [stored] when it was written for [fingerprint]; otherwise [detect], kept through [store]. */
internal fun cachedPhoneSystem(stored: String?, fingerprint: String, detect: () -> SystemKind, store: (String) -> Unit): SystemKind {
    val parts = stored?.split('\n')
    if (fingerprint.isNotEmpty() && parts?.size == 4 && parts[0] == fingerprint && parts[1].isNotBlank())
        return SystemKind(parts[1], parts[2], parts[3])
    val system = detect()
    // A value with a line break in it is not kept: it is detected again next time, as before.
    val fields = listOf(fingerprint, system.name, system.version, system.property)
    if (fingerprint.isNotEmpty() && fields.none { '\n' in it }) store(fields.joinToString("\n"))
    return system
}

internal object PhoneReadiness {
    // The names are ids: sent to the hub as the phone's OS and matched below; [systemName] is what a page shows.
    private const val Anco = "卓易通" // i18n-ignore: system id sent to the hub
    const val HyperOS = "澎湃 OS" // i18n-ignore: system id sent to the hub
    private const val Harmony = "鸿蒙" // i18n-ignore: system id sent to the hub
    private val systems = listOf(
        Anco to "ro.product.anco.devicetype", HyperOS to "ro.mi.os.version.name",
        "ColorOS" to "ro.build.version.oplusrom", "OriginOS" to "ro.vivo.os.build.display.id",
        "MagicOS" to "msc.config.magic.version", Harmony to "hw_sc.build.platform.version",
    )

    /** A system's name in the page's language (the id stays the same for the hub). */
    fun systemName(context: Context, name: String): String = when (name) {
        Anco -> context.getString(R.string.settings_system_anco)
        HyperOS -> context.getString(R.string.settings_system_hyperos)
        Harmony -> context.getString(R.string.settings_system_harmony)
        else -> name
    }
    fun system(): SystemKind {
        systems.forEach { (name, key) ->
            val value = runCatching {
                val process = ProcessBuilder("/system/bin/getprop", key).start()
                try {
                    if (process.waitFor(1, TimeUnit.SECONDS)) process.inputStream.bufferedReader().use { it.readText().trim() } else ""
                } finally { process.destroy() }
            }.getOrDefault("")
            if (value.isNotBlank()) return SystemKind(name, value, key)
        }
        return SystemKind("Android", Build.VERSION.RELEASE, "")
    }

    fun items(context: Context, system: SystemKind): List<ReadyItem> {
        val nm = context.getSystemService(NotificationManager::class.java)
        val pm = context.getSystemService(PowerManager::class.java)
        val am = context.getSystemService(ActivityManager::class.java)
        val result = mutableListOf(
            ReadyItem("notifications", context.getString(R.string.settings_ready_notifications), context.getString(R.string.settings_ready_notifications_detail), nm.areNotificationsEnabled(), listOf(notificationSettings)),
            ReadyItem("battery", context.getString(R.string.settings_ready_battery), context.getString(R.string.settings_ready_battery_detail), pm.isIgnoringBatteryOptimizations(context.packageName), listOf(SettingsTarget(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))),
            ReadyItem("background", context.getString(R.string.settings_ready_background), context.getString(R.string.settings_ready_background_detail), !am.isBackgroundRestricted, listOf(appDetails)),
        )
        nm.notificationChannels.forEach { channel ->
            val groupBlocked = channel.group?.let { nm.getNotificationChannelGroup(it)?.isBlocked } == true
            result += ReadyItem("channel:${channel.id}", channel.name.toString(), context.getString(R.string.settings_ready_channel_detail, channel.importance), nm.areNotificationsEnabled() && channel.importance > 0 && !groupBlocked,
                listOf(SettingsTarget("channel:${channel.id}")))
        }
        if (nm.notificationChannels.isEmpty()) result += ReadyItem("channels", context.getString(R.string.settings_ready_channels), context.getString(R.string.settings_ready_channels_detail), false, listOf(notificationSettings))
        val instruction = context.getString(when (system.name) {
            HyperOS -> R.string.settings_ready_vendor_hyperos
            "ColorOS" -> R.string.settings_ready_vendor_coloros
            "OriginOS" -> R.string.settings_ready_vendor_originos
            "MagicOS" -> R.string.settings_ready_vendor_magicos
            Harmony -> R.string.settings_ready_vendor_harmony
            Anco -> R.string.settings_ready_vendor_anco
            else -> R.string.settings_ready_vendor_other
        })
        result += ReadyItem("autostart:${system.name}", context.getString(R.string.settings_ready_autostart),
            context.getString(R.string.settings_ready_autostart_detail),
            null, vendorTargets[system.name].orEmpty())
        result += ReadyItem("vendor:${system.name}", context.getString(R.string.settings_ready_vendor, systemName(context, system.name), system.version), context.getString(R.string.settings_ready_vendor_detail, instruction), null, vendorTargets[system.name].orEmpty())
        result += ReadyItem("task-lock", context.getString(R.string.settings_ready_task_lock), context.getString(R.string.settings_ready_task_lock_detail), null, listOf(appDetails))
        if (Build.VERSION.SDK_INT >= 37 && context.applicationInfo.targetSdkVersion >= 37) {
            result += ReadyItem("local-network", context.getString(R.string.settings_ready_local_network), context.getString(R.string.settings_ready_local_network_detail), context.checkSelfPermission("android.permission.ACCESS_LOCAL_NETWORK") == android.content.pm.PackageManager.PERMISSION_GRANTED, listOf(appDetails))
        }
        return result
    }

    private val vendorTargets = mapOf(
        HyperOS to listOf(SettingsTarget(component = "com.miui.securitycenter/com.miui.permcenter.autostart.AutoStartManagementActivity")),
        "ColorOS" to listOf(SettingsTarget(component = "com.coloros.safecenter/com.coloros.safecenter.permission.startup.StartupAppListActivity")),
        "OriginOS" to listOf(SettingsTarget(component = "com.vivo.permissionmanager/com.vivo.permissionmanager.activity.BgStartUpManagerActivity")),
        "MagicOS" to listOf(SettingsTarget(component = "com.hihonor.systemmanager/com.hihonor.systemmanager.startupmgr.ui.StartupNormalAppListActivity")),
        Harmony to listOf(SettingsTarget(component = "com.huawei.systemmanager/com.huawei.systemmanager.startupmgr.ui.StartupNormalAppListActivity")),
    )

    fun open(context: Context, targets: List<SettingsTarget>): Boolean {
        for (target in targets + appDetails) {
            val intent = Intent().addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            target.component?.let { intent.component = ComponentName.unflattenFromString(it) }
            target.action?.let { action ->
                if (action.startsWith("channel:")) {
                    intent.action = Settings.ACTION_CHANNEL_NOTIFICATION_SETTINGS
                    intent.putExtra(Settings.EXTRA_CHANNEL_ID, action.removePrefix("channel:"))
                } else intent.action = action
            }
            intent.putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName)
            if (target.packageUri) intent.data = Uri.parse("package:${context.packageName}")
            try {
                context.startActivity(intent)
                Log.i("SettingsJump", "${target.action ?: target.component}: opened")
                return true
            } catch (_: android.content.ActivityNotFoundException) {
                Log.i("SettingsJump", "${target.action ?: target.component}: unavailable")
            } catch (_: SecurityException) {
                Log.i("SettingsJump", "${target.action ?: target.component}: denied")
            }
        }
        return false
    }

    fun network(context: Context): String = runCatching {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        val network = cm.activeNetwork ?: return context.getString(R.string.settings_network_none)
        val caps = cm.getNetworkCapabilities(network)
        val props = cm.getLinkProperties(network)
        val ipv6 = props?.linkAddresses?.any { val a = it.address; a is Inet6Address && !a.isLinkLocalAddress && !a.isLoopbackAddress && !a.isSiteLocalAddress && (a.address[0].toInt() and 0xe0) == 0x20 } == true &&
            props.routes.any { it.isDefaultRoute && it.destination.address is Inet6Address }
        context.getString(R.string.settings_network_summary,
            context.getString(if (ipv6) R.string.settings_network_ipv6_ok else R.string.settings_network_ipv6_none),
            context.getString(if (caps?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true) R.string.settings_yes else R.string.settings_no),
            context.getString(if (caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_CAPTIVE_PORTAL) == true) R.string.settings_network_portal_needed else R.string.settings_network_portal_none))
    }.getOrElse { context.getString(R.string.settings_network_unreadable) }
}
