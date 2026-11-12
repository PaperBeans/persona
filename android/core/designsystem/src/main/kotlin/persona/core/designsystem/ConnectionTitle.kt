package persona.core.designsystem

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/** Pages supply the current connection label and retain their normal title when online. */
@Composable
fun ConnectionTitle(title: String, busy: Boolean, modifier: Modifier = Modifier,
    leading: (@Composable () -> Unit)? = null,
) {
    Row(modifier, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp.dense)) {
        if (busy) JSpinner(Modifier.size(16.dp), stroke = 2.dp)
        else leading?.invoke()
        Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}
