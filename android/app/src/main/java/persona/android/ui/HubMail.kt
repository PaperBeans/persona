package persona.android.ui

import persona.core.designsystem.MailAccess
import persona.core.moduleapi.HubModule
import persona.core.moduleapi.ModuleRegistry

/**
 * [MailAccess] for a hub whose hello offers [features] (#15995): the one place the app reads the mail module's flag, by
 * the rule every module follows ([ModuleRegistry.isActive]). Off only on a hub that publishes module flags without
 * `module.mail`.
 */
internal fun mailAccessOf(features: Set<String>): MailAccess =
    if (ModuleRegistry.isActive(HubModule.Mail, features)) MailAccess.Live else MailAccess.StoredOnly
