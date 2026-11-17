package io.github.sidd065.agentcompanion.ui

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Tune
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import io.github.sidd065.agentcompanion.core.AppSettings
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.service.Notifier
import io.github.sidd065.agentcompanion.ui.chat.ChatScreen
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.files.FileScreen
import io.github.sidd065.agentcompanion.ui.files.FilesScreen
import io.github.sidd065.agentcompanion.ui.manage.ManageScreen
import io.github.sidd065.agentcompanion.ui.manage.TextViewerScreen
import io.github.sidd065.agentcompanion.ui.sessions.DiffScreen
import io.github.sidd065.agentcompanion.ui.sessions.NewSessionScreen
import io.github.sidd065.agentcompanion.ui.sessions.SessionsScreen
import io.github.sidd065.agentcompanion.ui.settings.HostsScreen
import io.github.sidd065.agentcompanion.ui.settings.SettingsScreen
import io.github.sidd065.agentcompanion.ui.terminal.TerminalScreen
import io.github.sidd065.agentcompanion.ui.terminal.TerminalsScreen
import io.github.sidd065.agentcompanion.ui.theme.AppTheme
import kotlinx.coroutines.flow.MutableStateFlow
import java.net.URLDecoder
import java.net.URLEncoder

val LocalNav = staticCompositionLocalOf<NavHostController> { error("no nav") }

fun enc(s: String): String = URLEncoder.encode(s, "UTF-8")
fun dec(s: String?): String = s?.let { URLDecoder.decode(it, "UTF-8") } ?: ""

/**
 * The host of the screen on top. Host-scoped destinations set it while they're
 * shown, so routes built from inside them stay on the same host.
 */
object CurrentHost {
    @Volatile var id: String? = null
}

val LocalHostId = staticCompositionLocalOf<String?> { null }

object Routes {
    const val SESSIONS = "sessions"
    const val FILES = "files"
    const val TERMS = "terms"
    const val MANAGE = "manage"
    private fun h(h: String?) = "h=" + enc(h ?: CurrentHost.id ?: "")
    fun chat(id: String, prefill: String = "", host: String? = null) = "chat/$id?prefill=${enc(prefill)}&${h(host)}"
    fun newSession(cwd: String = "", text: String = "", host: String? = null) = "new?cwd=${enc(cwd)}&text=${enc(text)}&${h(host)}"
    fun file(path: String, edit: Boolean = false, host: String? = null) = "file?path=${enc(path)}&edit=$edit&${h(host)}"
    fun files(path: String, host: String? = null) = "files?path=${enc(path)}&${h(host)}"
    fun term(id: String, host: String? = null) = "term/$id?${h(host)}"
    fun diff(sessionId: String = "-", path: String = "", host: String? = null) = "diff/$sessionId?path=${enc(path)}&${h(host)}"
    fun artifact(sessionId: String, path: String, host: String? = null) = "artifact/$sessionId?path=${enc(path)}&${h(host)}"
    fun text(key: String) = "text/$key"
    fun select(key: String) = "select/$key"
    fun asides(sessionId: String, host: String? = null) = "asides/$sessionId?${h(host)}"
}

/** Provides [hostId] (blank = the selected host) to a host-scoped screen. */
@Composable
fun HostScope(hostId: String?, content: @Composable () -> Unit) {
    val g = LocalContext.current.graph
    val settings by g.prefs.flow.collectAsState()
    val id = hostId?.takeIf { it.isNotBlank() && settings.hosts.any { h -> h.id == it } } ?: settings.activeHost?.id
    androidx.compose.runtime.SideEffect { CurrentHost.id = id }
    CompositionLocalProvider(LocalHostId provides id) { content() }
}

/** Large text payloads handed to the text viewer route. */
object TextHolder {
    val items = HashMap<String, Pair<String, String>>() // key -> (title, text)
    fun put(title: String, text: String): String {
        val k = System.nanoTime().toString()
        items[k] = title to text
        return k
    }
}

/** Intents from notifications/share that the UI should act on. */
object PendingNav {
    val intent = MutableStateFlow<Intent?>(null)
    /** A pairing link opened from outside (agentc://pair…). */
    val pairLink = MutableStateFlow<String?>(null)
}

@Composable
fun AppRoot() {
    val ctx = LocalContext.current
    val g = ctx.graph
    val settings by g.prefs.flow.collectAsState()
    AppTheme(theme = settings.theme, dynamic = settings.dynamicColor, textScale = settings.textScale) {
        val nav = rememberNavController()
        val snack = remember { SnackbarHostState() }
        val external = LocalUriHandler.current
        val uriHandler = remember(nav) {
            object : UriHandler {
                override fun openUri(uri: String) {
                    if (uri.startsWith("file://") || uri.startsWith("/")) {
                        val path = Uri.decode(uri.removePrefix("file://").substringBefore('#'))
                        nav.navigate(Routes.file(path))
                    } else {
                        runCatching { external.openUri(uri) }
                    }
                }
            }
        }
        CompositionLocalProvider(LocalNav provides nav, LocalSnack provides snack, LocalUriHandler provides uriHandler) {
            if (settings.hosts.isEmpty()) {
                HostsScreen(firstRun = true)
            } else {
                MainNav(nav, settings)
            }
            io.github.sidd065.agentcompanion.ui.settings.PendingPairing()
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.BottomCenter) { SnackbarHost(snack) }
        }
    }
}

/** The connection of the current host scope (or the selected host). */
@Composable
fun activeConnection(): HostConnection? {
    val g = LocalContext.current.graph
    val s by g.prefs.flow.collectAsState()
    val conns by g.connections.collectAsState()
    val id = LocalHostId.current ?: s.activeHost?.id
    return conns.firstOrNull { it.config.id == id } ?: conns.firstOrNull()
}

private data class Tab(val route: String, val label: String, val icon: androidx.compose.ui.graphics.vector.ImageVector)

private val tabs = listOf(
    Tab(Routes.SESSIONS, "Sessions", Icons.AutoMirrored.Outlined.Chat),
    Tab(Routes.FILES, "Files", Icons.Outlined.Folder),
    Tab(Routes.TERMS, "Terminals", Icons.Outlined.Terminal),
    Tab(Routes.MANAGE, "Host", Icons.Outlined.Tune),
)

@Composable
fun BottomTabs(nav: NavHostController) {
    val entry by nav.currentBackStackEntryAsState()
    val route = entry?.destination?.route?.substringBefore('?') ?: ""
    NavigationBar {
        tabs.forEach { t ->
            NavigationBarItem(
                selected = route == t.route,
                onClick = {
                    if (route != t.route) {
                        nav.navigate(t.route) {
                            popUpTo(nav.graph.findStartDestination().id) { saveState = true }
                            launchSingleTop = true
                            restoreState = true
                        }
                    }
                },
                icon = { Icon(t.icon, null) },
                label = { Text(t.label) },
            )
        }
    }
}

@Composable
private fun MainNav(nav: NavHostController, settings: AppSettings) {
    val ctx = LocalContext.current
    val g = ctx.graph
    val pending by PendingNav.intent.collectAsState()
    LaunchedEffect(pending) {
        val i = pending ?: return@LaunchedEffect
        PendingNav.intent.value = null
        val host = i.getStringExtra(Notifier.EXTRA_HOST)?.takeIf { h -> settings.hosts.any { it.id == h } }
        val sid = i.getStringExtra(Notifier.EXTRA_SESSION)
        val tid = i.getStringExtra(Notifier.EXTRA_TERM)
        when {
            sid != null -> {
                nav.navigate(Routes.chat(sid, host = host)) { launchSingleTop = true }
                // A side answer opens on its own screen, above the chat.
                if (i.getStringExtra(Notifier.EXTRA_KIND) == "aside") nav.navigate(Routes.asides(sid, host = host))
            }
            tid != null -> nav.navigate(Routes.term(tid, host = host)) { launchSingleTop = true }
            i.action == Intent.ACTION_SEND -> {
                val text = i.getStringExtra(Intent.EXTRA_TEXT).orEmpty()
                ShareInbox.image = if (i.type?.startsWith("image/") == true) {
                    @Suppress("DEPRECATION") i.getParcelableExtra<Uri>(Intent.EXTRA_STREAM)
                } else null
                nav.navigate(Routes.newSession(text = text))
            }
        }
    }
    val hArg = navArgument("h") { type = NavType.StringType; defaultValue = "" }
    NavHost(nav, startDestination = Routes.SESSIONS) {
        composable(Routes.SESSIONS) { androidx.compose.runtime.SideEffect { CurrentHost.id = null }; SessionsScreen() }
        composable(Routes.FILES) { HostScope(null) { androidx.compose.runtime.key(LocalHostId.current) { FilesScreen(initialPath = "") } } }
        composable("files?path={path}&h={h}", arguments = listOf(navArgument("path") { type = NavType.StringType; defaultValue = "" }, hArg)) {
            HostScope(it.arguments?.getString("h")) { FilesScreen(initialPath = dec(it.arguments?.getString("path")), standalone = true) }
        }
        composable(Routes.TERMS) { androidx.compose.runtime.SideEffect { CurrentHost.id = null }; TerminalsScreen() }
        composable(Routes.MANAGE) { HostScope(null) { ManageScreen() } }
        composable("settings") { SettingsScreen() }
        composable("hosts") { HostsScreen(firstRun = false) }
        composable(
            "chat/{sid}?prefill={prefill}&h={h}",
            arguments = listOf(navArgument("sid") { type = NavType.StringType }, navArgument("prefill") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                ChatScreen(sessionId = it.arguments?.getString("sid") ?: "", prefill = dec(it.arguments?.getString("prefill")))
            }
        }
        composable(
            "new?cwd={cwd}&text={text}&h={h}",
            arguments = listOf(navArgument("cwd") { type = NavType.StringType; defaultValue = "" }, navArgument("text") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            NewSessionScreen(initialCwd = dec(it.arguments?.getString("cwd")), initialText = dec(it.arguments?.getString("text")),
                initialHost = it.arguments?.getString("h").orEmpty())
        }
        composable(
            "file?path={path}&edit={edit}&h={h}",
            arguments = listOf(navArgument("path") { type = NavType.StringType; defaultValue = "" }, navArgument("edit") { type = NavType.BoolType; defaultValue = false }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                FileScreen(path = dec(it.arguments?.getString("path")), startEditing = it.arguments?.getBoolean("edit") == true)
            }
        }
        composable("term/{tid}?h={h}", arguments = listOf(navArgument("tid") { type = NavType.StringType }, hArg)) {
            HostScope(it.arguments?.getString("h")) { TerminalScreen(termId = it.arguments?.getString("tid") ?: "") }
        }
        composable(
            "diff/{sid}?path={path}&h={h}",
            arguments = listOf(navArgument("sid") { type = NavType.StringType }, navArgument("path") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                DiffScreen(sessionId = it.arguments?.getString("sid")?.takeIf { s -> s != "-" }, path = dec(it.arguments?.getString("path")))
            }
        }
        composable(
            "artifact/{sid}?path={path}&h={h}",
            arguments = listOf(navArgument("sid") { type = NavType.StringType }, navArgument("path") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                io.github.sidd065.agentcompanion.ui.chat.ArtifactScreen(sessionId = it.arguments?.getString("sid") ?: "", path = dec(it.arguments?.getString("path")))
            }
        }
        composable("text/{key}", arguments = listOf(navArgument("key") { type = NavType.StringType })) {
            TextViewerScreen(key = it.arguments?.getString("key") ?: "")
        }
        composable("select/{key}", arguments = listOf(navArgument("key") { type = NavType.StringType })) {
            io.github.sidd065.agentcompanion.ui.components.SelectTextScreen(key = it.arguments?.getString("key") ?: "")
        }
        composable("asides/{sid}?h={h}", arguments = listOf(navArgument("sid") { type = NavType.StringType }, hArg)) {
            HostScope(it.arguments?.getString("h")) {
                io.github.sidd065.agentcompanion.ui.chat.SideQuestionsScreen(sessionId = it.arguments?.getString("sid") ?: "")
            }
        }
    }
}

/** Image shared into the app, picked up by the new-session screen. */
object ShareInbox {
    var image: Uri? = null
}
