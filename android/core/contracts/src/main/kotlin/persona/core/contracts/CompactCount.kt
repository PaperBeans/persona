package persona.core.contracts

import java.math.BigDecimal
import java.math.RoundingMode

/**
 * [count] as [CoreText.Key.CompactCount] reads it in a language that counts in [unit]s (a thousand in English, ten
 * thousand in Chinese and Japanese): under one unit the number itself, else [inUnits] of the count in units with one
 * decimal at most (181000 / 10000 → "18.1"). A language that counts in thousands steps up to millions ([inMillions], when
 * it has the words) once the count rounds to a thousand thousands, as the web and iOS do: 1,200,000 → "1.2", not "1200",
 * and 999,950 → "1", not "1000".
 */
fun compactCount(count: Long, unit: Long, inUnits: (String) -> String, inMillions: ((String) -> String)? = null): String {
    if (count < unit) return count.toString()
    val units = rounded(count, unit)
    if (inMillions != null && unit == 1_000L && units >= BigDecimal(1_000)) return inMillions(plain(rounded(count, 1_000_000)))
    return inUnits(plain(units))
}

private fun rounded(count: Long, unit: Long) = BigDecimal(count).divide(BigDecimal(unit)).setScale(1, RoundingMode.HALF_UP)
private fun plain(value: BigDecimal) = value.stripTrailingZeros().toPlainString()
