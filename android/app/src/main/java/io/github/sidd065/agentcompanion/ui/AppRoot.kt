package io.github.sidd065.agentcompanion.ui

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.unit.dp
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
import io.github.sidd065.agentcompanion.core.Prefs
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.ConnState
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
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch

val LocalNav = staticCompositionLocalOf<NavHostController> { error("no nav") }

/** Percent-encodes a query argument for Jetpack Navigation routes (`%20` for spaces, `%2B` for `+`, `%25` for `%`). */
fun enc(s: String): String = Uri.encode(s)

/**
 * Single-pass inverse of [enc] for raw percent-encoded route substrings.
 * Do NOT pass `NavBackStackEntry.arguments` values here — `NavController` already calls `Uri.decode` once.
 */
fun dec(s: String?): String = s?.let { Uri.decode(it) }.orEmpty()

internal fun android.os.Bundle?.navString(key: String): String = this?.getString(key).orEmpty()

internal fun resolveLocalFileUriPath(uri: String): String? = when {
    uri.startsWith("file://") -> Uri.parse(uri).path ?: uri.removePrefix("file://").substringBefore('#')
    uri.startsWith("/") -> uri.substringBefore('#')
    else -> null
}

/**
 * The host of the screen on top. Host-scoped destinations set it while they're
 * shown, so routes built from inside them stay on the same host.
 */
object CurrentHost {
    @Volatile var id: String? = null
}

val LocalHostId = staticCompositionLocalOf<String?> { null }

/** Tracks a host explicitly picked by the user in this app session (even if currently offline). */
object ExplicitHostChoice {
    private val _flow = MutableStateFlow<String?>(null)
    val flow: StateFlow<String?> = _flow

    var hostId: String?
        get() = _flow.value
        set(value) { _flow.value = value }
}

fun selectActiveHost(prefs: Prefs, scope: CoroutineScope, hostId: String) {
    ExplicitHostChoice.hostId = hostId
    scope.launch { prefs.update { it.copy(activeHostId = hostId) } }
}

fun resolveEffectiveHostId(
    explicitRouteHostId: String?,
    settings: AppSettings,
    conns: List<HostConnection>,
    userPickedHostId: String? = ExplicitHostChoice.hostId,
): String? {
    val validRoute = explicitRouteHostId?.takeIf { id -> id.isNotBlank() && settings.hosts.any { it.id == id } }
    if (validRoute != null) return validRoute
    val picked = userPickedHostId?.takeIf { id -> id.isNotBlank() && settings.hosts.any { it.id == id } }
    val saved = settings.activeHostId?.takeIf { id -> settings.hosts.any { it.id == id } }
    if (saved != null) {
        val savedConn = conns.firstOrNull { it.config.id == saved }
        if (savedConn?.rpc?.isConnected == true || picked == saved) return saved
    }
    if (picked != null && settings.activeHostId == null) return picked
    return conns.firstOrNull { it.rpc.isConnected }?.config?.id
        ?: saved
        ?: settings.hosts.firstOrNull()?.id
}

@Composable
private fun observeConnStates(conns: List<HostConnection>): List<ConnState> =
    conns.map { c ->
        val st by androidx.compose.runtime.key(c.config.id) { c.rpc.state.collectAsState() }
        st
    }

object Routes {
    const val SESSIONS = "sessions"
    const val FILES = "files"
    const val TERMS = "terms"
    const val MANAGE = "manage"
    private fun h(h: String?) = "h=" + enc(h ?: CurrentHost.id ?: "")
    fun chat(id: String, prefill: String = "", host: String? = null) = "chat/$id?prefill=${enc(prefill)}&${h(host)}"
    fun newSession(cwd: String = "", text: String = "", host: String? = null) = "new?cwd=${enc(cwd)}&text=${enc(text)}&${h(host)}"
    fun file(path: String, edit: Boolean = false, host: String? = null) = "file?path=${enc(path)}&edit=$edit&${h(host)}"
    fun files(path: String, host: String? = null) = "folder?path=${enc(path)}&${h(host)}"
    fun term(id: String, host: String? = null) = "term/$id?${h(host)}"
    fun diff(sessionId: String = "-", path: String = "", host: String? = null) = "diff/$sessionId?path=${enc(path)}&${h(host)}"
    fun artifact(sessionId: String, path: String, host: String? = null) = "artifact/$sessionId?path=${enc(path)}&${h(host)}"
    fun text(key: String) = "text/$key"
    fun select(key: String) = "select/$key"
    fun asides(sessionId: String, host: String? = null) = "asides/$sessionId?${h(host)}"
}

internal const val ROUTE_FOLDER = "folder?path={path}&h={h}"
internal const val ROUTE_CHAT = "chat/{sid}?prefill={prefill}&h={h}"
internal const val ROUTE_NEW = "new?cwd={cwd}&text={text}&h={h}"
internal const val ROUTE_FILE = "file?path={path}&edit={edit}&h={h}"
internal const val ROUTE_TERM = "term/{tid}?h={h}"
internal const val ROUTE_DIFF = "diff/{sid}?path={path}&h={h}"
internal const val ROUTE_ARTIFACT = "artifact/{sid}?path={path}&h={h}"

/** Provides [hostId] (blank = the selected host) to a host-scoped screen. */
@Composable
fun HostScope(hostId: String?, content: @Composable () -> Unit) {
    val g = LocalContext.current.graph
    val settings by g.prefs.flow.collectAsState()
    val conns by g.connections.collectAsState()
    val explicit by ExplicitHostChoice.flow.collectAsState()
    observeConnStates(conns)
    val id = resolveEffectiveHostId(hostId, settings, conns, explicit)
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
                    val path = resolveLocalFileUriPath(uri)
                    if (path != null) {
                        nav.navigate(Routes.file(path, host = CurrentHost.id))
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
            Box(Modifier.fillMaxSize().navigationBarsPadding().imePadding().padding(bottom = 12.dp), contentAlignment = Alignment.BottomCenter) { SnackbarHost(snack) }
        }
    }
}

/** The connection of the current host scope (or the selected host). */
@Composable
fun activeConnection(): HostConnection? {
    val g = LocalContext.current.graph
    val s by g.prefs.flow.collectAsState()
    val conns by g.connections.collectAsState()
    val explicit by ExplicitHostChoice.flow.collectAsState()
    observeConnStates(conns)
    val id = LocalHostId.current ?: resolveEffectiveHostId(null, s, conns, explicit)
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
        composable(Routes.FILES) { HostScope(null) { FilesScreen(initialPath = "") } }
        composable(ROUTE_FOLDER, arguments = listOf(navArgument("path") { type = NavType.StringType; defaultValue = "" }, hArg)) {
            HostScope(it.arguments?.getString("h")) {
                androidx.compose.runtime.key(LocalHostId.current) {
                    FilesScreen(initialPath = it.arguments.navString("path"), standalone = true)
                }
            }
        }
        composable(Routes.TERMS) { androidx.compose.runtime.SideEffect { CurrentHost.id = null }; TerminalsScreen() }
        composable(Routes.MANAGE) { HostScope(null) { androidx.compose.runtime.key(LocalHostId.current) { ManageScreen() } } }
        composable("settings") { SettingsScreen() }
        composable("hosts") { HostsScreen(firstRun = false) }
        composable(
            ROUTE_CHAT,
            arguments = listOf(navArgument("sid") { type = NavType.StringType }, navArgument("prefill") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                ChatScreen(sessionId = it.arguments.navString("sid"), prefill = it.arguments.navString("prefill"))
            }
        }
        composable(
            ROUTE_NEW,
            arguments = listOf(navArgument("cwd") { type = NavType.StringType; defaultValue = "" }, navArgument("text") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            NewSessionScreen(
                initialCwd = it.arguments.navString("cwd"),
                initialText = it.arguments.navString("text"),
                initialHost = it.arguments.navString("h"),
            )
        }
        composable(
            ROUTE_FILE,
            arguments = listOf(navArgument("path") { type = NavType.StringType; defaultValue = "" }, navArgument("edit") { type = NavType.BoolType; defaultValue = false }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                androidx.compose.runtime.key(LocalHostId.current) {
                    FileScreen(path = it.arguments.navString("path"), startEditing = it.arguments?.getBoolean("edit") == true)
                }
            }
        }
        composable(ROUTE_TERM, arguments = listOf(navArgument("tid") { type = NavType.StringType }, hArg)) {
            HostScope(it.arguments?.getString("h")) { TerminalScreen(termId = it.arguments.navString("tid")) }
        }
        composable(
            ROUTE_DIFF,
            arguments = listOf(navArgument("sid") { type = NavType.StringType }, navArgument("path") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                DiffScreen(sessionId = it.arguments?.getString("sid")?.takeIf { s -> s != "-" }, path = it.arguments.navString("path"))
            }
        }
        composable(
            ROUTE_ARTIFACT,
            arguments = listOf(navArgument("sid") { type = NavType.StringType }, navArgument("path") { type = NavType.StringType; defaultValue = "" }, hArg),
        ) {
            HostScope(it.arguments?.getString("h")) {
                io.github.sidd065.agentcompanion.ui.chat.ArtifactScreen(sessionId = it.arguments.navString("sid"), path = it.arguments.navString("path"))
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
