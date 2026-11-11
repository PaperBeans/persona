package persona.core.contracts.api

import persona.core.contracts.Machine
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf

/** A signed-in device adds a computer: `POST /api/v1/auth/pair-codes {"add"}` (docs/protocol/protocol.md "Computer pairing codes"). */
const val MachinesAddFeature = "machines_add"

/** Machines carry `trust` / `error_code` and take `…/confirm` / `…/reject` (docs/protocol/protocol.md "Computer trust"). */
const val MachineTrustFeature = "machine_trust"

/** The systems "Add computer" has instructions for; the hub takes the same words (`add.os`). */
enum class ComputerOs(val wire: String) {
    Mac("mac"), Linux("linux"), Windows("windows");

    companion object {
        /** A machine's system from what its node reported (darwin, linux…), null when unknown. */
        fun of(reported: String?): ComputerOs? {
            val os = reported.orEmpty().lowercase()
            return when {
                os.startsWith("linux") -> Linux
                os.startsWith("windows") -> Windows
                os.startsWith("darwin") || os.startsWith("mac") -> Mac
                else -> null
            }
        }
    }
}

/**
 * A computer's pairing code: the link its Persona Link takes ([pairUrl]), [machine] in the ids the app's lists use.
 * [command] pairs an installed persona-link; [linuxInstall] installs and pairs in one line, only when the hub knows the
 * site it was published from.
 */
data class ComputerCode(
    val machine: String,
    val name: String,
    val pairUrl: String,
    /** Epoch milliseconds; 0 when unreadable. */
    val expiresAt: Long,
    val trust: String = "pending",
    val added: Boolean = false,
    val command: String? = null,
    val linuxInstall: String? = null,
    /**
     * The manager the computer belongs to now (docs/protocol/protocol.md "Computer pairing codes"); null from a hub before
     * `add.manager`, which puts a new computer under its own assistant (`assistant`).
     */
    val manager: String? = null,
)

/**
 * Machines › "Add computer" and "Confirm computer" (docs/assistant/onboarding.md 4.2 "Your computer <-> hub"). Adding goes to the selected hub; a machine id
 * (a code for one again, confirm, reject) goes to the hub its id names.
 */
interface ComputerPairing {
    /** The selected hub has [MachinesAddFeature]. */
    val canAdd: Flow<Boolean> get() = flowOf(false)

    /** A new computer named [name] (up to 40 characters, any language), with its code. */
    suspend fun add(name: String, os: ComputerOs): ComputerCode

    /**
     * The same, the computer [manager]'s from the start (`add.manager`, a manager's id from the roster; null: none named,
     * the hub's own assistant's as before).
     */
    suspend fun add(name: String, os: ComputerOs, manager: String?): ComputerCode = add(name, os)

    /** A new code for a computer that waits to be confirmed (the old one's Persona Link is revoked). */
    suspend fun again(machineId: String): ComputerCode

    /** `GET /api/v1/machines` of the hub [machineId] belongs to, read again while waiting for it to come online. */
    suspend fun machines(machineId: String): List<Machine>

    /** `POST /api/v1/machines/{id}/confirm`: it is mine; the hub then gives it the keys. */
    suspend fun confirm(machineId: String)

    /** `POST /api/v1/machines/{id}/reject`: not mine; the hub removes it and revokes its Persona Link. */
    suspend fun reject(machineId: String)
}
