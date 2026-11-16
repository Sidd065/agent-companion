package io.github.sidd065.agentcompanion

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import io.github.sidd065.agentcompanion.ui.AppRoot
import io.github.sidd065.agentcompanion.ui.PendingNav

class MainActivity : ComponentActivity() {
    private val notifPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        handle(intent)
        if (Build.VERSION.SDK_INT >= 33) notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        setContent { AppRoot() }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    override fun onResume() {
        super.onResume()
        graph.reconnectAll()
    }

    private fun handle(i: Intent?) {
        if (i == null) return
        if (i.action == Intent.ACTION_VIEW && i.data?.scheme == "agentc") {
            PendingNav.pairLink.value = i.dataString
            return
        }
        if (i.hasExtra("session") || i.hasExtra("term") || i.action == Intent.ACTION_SEND) {
            PendingNav.intent.value = Intent(i)
        }
    }
}
