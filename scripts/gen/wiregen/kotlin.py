"""Kotlin: kotlinx.serialization data classes with defaults (a missing field takes its zero value), unions as sealed
interfaces with a serializer that reads the discriminator and keeps what it does not know as Unknown."""

from __future__ import annotations

import json
import re

from .model import DEFAULT_HEADER, Def, SchemaError, TypeRef, reserved_keys, unknown_doc
from .names import camel, comment, lower_camel, plural, sentence, upper_snake


KOTLIN_KEYWORDS = {"as", "break", "class", "continue", "do", "else", "false", "for", "fun", "if", "in", "interface",
                   "is", "null", "object", "package", "return", "super", "this", "throw", "true", "try", "typealias",
                   "typeof", "val", "var", "when", "while"}
KOTLIN_PACKAGE = "persona.core.contracts.generated"


def kt_ident(s: str) -> str:
    return f"`{s}`" if s in KOTLIN_KEYWORDS else s


def kt_case(value: str) -> str:
    """A closed enum's object: UpperCamel, with a suffix where it would be the enum's own Unknown."""
    n = camel(value)
    return n + "Value" if n == "Unknown" else n


def gen_kotlin(defs: dict[str, Def], *, header: list[str] | None = None, prefix: str = "Extension",
               package: str = KOTLIN_PACKAGE, defects: str = "ExtensionWire") -> str:
    """defects names the object whose unknownEnum hook closed enums with x-unknown "count" call (<defects>Defects)."""
    def kname(name: str) -> str:
        return name if name.startswith(prefix) else prefix + name

    out = [*(header or DEFAULT_HEADER), "",
           f"package {package}", "",
           "import kotlinx.serialization.KSerializer",
           "import kotlinx.serialization.SerialName",
           "import kotlinx.serialization.Serializable",
           "import kotlinx.serialization.SerializationException",
           "import kotlinx.serialization.descriptors.PrimitiveKind",
           "import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor",
           "import kotlinx.serialization.descriptors.SerialDescriptor",
           "import kotlinx.serialization.encoding.Decoder",
           "import kotlinx.serialization.encoding.Encoder",
           "import kotlinx.serialization.json.Json",
           "import kotlinx.serialization.json.JsonDecoder",
           "import kotlinx.serialization.json.JsonElement",
           "import kotlinx.serialization.json.JsonEncoder",
           "import kotlinx.serialization.json.JsonNull",
           "import kotlinx.serialization.json.JsonObject",
           "import kotlinx.serialization.json.JsonPrimitive",
           "import kotlinx.serialization.json.contentOrNull",
           "import kotlinx.serialization.json.jsonPrimitive",
           ""]

    def ktype(t: TypeRef) -> str:
        k = t.kind
        if k in ("string", "time"):
            return "String"
        if k == "int":
            return "Int"
        if k == "int64":
            return "Long"
        if k == "number":
            return "Double"
        if k == "bool":
            return "Boolean"
        if k == "any":
            return "JsonElement"
        if k == "array":
            return f"List<{ktype(t.of)}>"
        if k == "map":
            return f"Map<String, {ktype(t.of)}>"
        d = defs[t.ref]
        return "String" if d.kind == "enum" and not d.closed else kname(t.ref)

    def zero(t: TypeRef) -> str:
        k = t.kind
        if k in ("string", "time"):
            return '""'
        if k == "int":
            return "0"
        if k == "int64":
            return "0L"
        if k == "number":
            return "0.0"
        if k == "bool":
            return "false"
        if k == "any":
            return "JsonNull"
        if k == "array":
            return "emptyList()"
        if k == "map":
            return "emptyMap()"
        d = defs[t.ref]
        if d.kind in ("any", "opaque"):
            return "JsonNull"
        if d.kind == "enum":
            return f'{kname(t.ref)}.Unknown("")' if d.closed else '""'
        if d.kind in ("id", "text"):
            return f'{kname(t.ref)}("")'
        if d.kind == "union":
            raise SchemaError(f"a required field of union type {t.ref}: not in the subset")
        return f"{kname(t.ref)}()"

    def kdoc(text: str, indent: str = "") -> list[str]:
        if not text:
            return []
        return [f"{indent}/**"] + comment(f"{indent} * ", text) + [f"{indent} */"]

    hook = defects + "Defects"
    if any(d.count_unknown for d in defs.values()):
        out += kdoc("Called with a closed enum's name and the raw value each time one with x-unknown \"count\" decodes "
                    "a value this app does not know. null: nothing is called.")
        out += [f"object {hook} {{", "    @Volatile", "    var unknownEnum: ((enumName: String, raw: String) -> Unit)? = null",
                "}", ""]

    def closed_enum(d: Def) -> list[str]:
        name = kname(d.name)
        cases = [(v, kt_case(v)) for v in d.values]
        lines = kdoc((d.doc or f"{d.name}: a closed set of values.") + " A value this app does not know decodes to "
                     "Unknown with the raw value, and encodes back as it came.")
        lines += [f"@Serializable(with = {name}Serializer::class)", f"sealed interface {name} {{", "    val raw: String", ""]
        lines += [f'    data object {c} : {name} {{ override val raw: String = {json.dumps(v)} }}' for v, c in cases]
        lines += ["", "    /** A value this app does not know, kept as it came. */",
                  f"    data class Unknown(override val raw: String) : {name}", "",
                  "    companion object {",
                  f"        val ALL: List<{name}> = listOf({', '.join(name + '.' + c for _, c in cases)})", "",
                  f"        fun of(raw: String): {name} = when (raw) {{"]
        lines += [f"            {json.dumps(v)} -> {name}.{c}" for v, c in cases]
        lines += [f"            else -> {name}.Unknown(raw)", "        }", "    }", "}", "",
                  f"object {name}Serializer : KSerializer<{name}> {{",
                  f"    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor(\"{package}.{name}\", "
                  "PrimitiveKind.STRING)", "",
                  f"    override fun deserialize(decoder: Decoder): {name} {{", "        val raw = decoder.decodeString()",
                  f"        val value = {name}.of(raw)"]
        if d.count_unknown:
            lines += [f"        if (value is {name}.Unknown) {hook}.unknownEnum?.invoke({json.dumps(d.name)}, raw)"]
        lines += ["        return value", "    }", "",
                  f"    override fun serialize(encoder: Encoder, value: {name}) {{", "        encoder.encodeString(value.raw)",
                  "    }", "}", ""]
        return lines

    for d in defs.values():
        if d.external:
            continue  # a shared file's ID: its own file, in this package, declares it
        if d.kind == "enum" and d.closed:
            out += closed_enum(d)
        elif d.kind in ("id", "text"):
            what = "a label (free text, not a closed set)" if d.kind == "text" else "an ID"
            out += kdoc((d.doc or f"{d.name}: {what}.") + " It encodes as the bare string.")
            out += ["@Serializable", "@JvmInline", f"value class {kname(d.name)}(val raw: String) {{",
                    "    override fun toString(): String = raw", "}", ""]
        elif d.kind == "opaque":
            out += kdoc(" ".join(x for x in (d.doc, f"Opaque JSON: {sentence(d.reason)}") if x))
            out += [f"typealias {kname(d.name)} = JsonElement", ""]
        elif d.kind == "enum":
            out += kdoc(d.doc or f"Values of {d.name}.")
            out.append(f"object {kname(d.name)} {{")
            for v in d.values:
                # A value named like the list of values ("all") takes a suffix, as in Swift.
                out.append(f'    const val {upper_snake(v) + ("_VALUE" if upper_snake(v) == "ALL" else "")} = "{v}"')
            out.append(f"    val ALL = listOf({', '.join(json.dumps(v) for v in d.values)})")
            out += ["}", ""]
        elif d.kind == "any":
            out += kdoc(d.doc) + [f"typealias {kname(d.name)} = JsonElement", ""]
        elif d.kind == "struct":
            name = kname(d.name)
            out += kdoc(d.doc)
            custom = d.extra or d.preserve_required
            out.append(f"@Serializable(with = {name}Serializer::class)" if custom else "@Serializable")
            params = []
            if d.union:
                disc = defs[d.union].discriminator
                params.append(f"    override val {kt_ident(lower_camel(disc))}: String = {json.dumps(d.tag)},")
            for f in d.fields:
                n = lower_camel(f.name)
                ser = f'@SerialName("{f.name}") ' if n != f.name else ""
                typ = ktype(f.type)
                params += kdoc(f.doc, "    ")
                if f.required and d.strict:
                    # x-decode "strict": a missing required field fails to decode instead of taking its zero value.
                    params.append(f"    {ser}val {kt_ident(n)}: {typ},")
                elif f.required:
                    params.append(f"    {ser}val {kt_ident(n)}: {typ} = {zero(f.type)},")
                else:
                    params.append(f"    {ser}val {kt_ident(n)}: {typ}? = null,")
            suffix = f" : {kname(d.union)}" if d.union else ""
            if params or d.extra:
                out.append(f"data class {name}(")
                out += params
                if d.extra:
                    out += ["    /** Extra keys stay at this object's level on the wire; declared fields always win. */",
                            "    val additionalProperties: Map<String, JsonElement> = emptyMap(),"]
                if d.keyed:
                    # A struct made from an enum-keyed map, read by its key: an unknown one has the zero value.
                    key = kname(d.keyed)
                    out.append(f"){suffix} {{")
                    out += kdoc(f"The field of one {d.keyed}: an unknown one reads {zero(d.fields[0].type)}.", "    ")
                    out.append(f"    operator fun get(key: {key}): {ktype(d.fields[0].type)} = when (key) {{")
                    out += [f"        {key}.{kt_case(f.name)} -> {kt_ident(lower_camel(f.name))}" for f in d.fields]
                    out += [f"        is {key}.Unknown -> {zero(d.fields[0].type)}", "    }", "}"]
                else:
                    out.append(f"){suffix}")
            else:
                out.append(f"class {name}{suffix}")
            out.append("")
            if custom:
                # A separate, generated class avoids recursive serialization. Stream encoding preserves required
                # defaults throughout its native payload without changing legacy objects or the caller's codec.
                known_name = name + "Known"
                keys = ", ".join(json.dumps(k) for k in reserved_keys(defs, d))
                out += ["@Serializable"]
                if params:
                    out += [f"private data class {known_name}("]
                    out += [p.replace("override val ", "val ") for p in params]
                    out += [")", ""]
                else:
                    out += [f"private class {known_name}", ""]
                out += [f"object {name}Serializer : KSerializer<{name}> {{",
                        f"    private val known = {known_name}.serializer()",
                        f"    private val keys = setOf<String>({keys})",
                        "    override val descriptor: SerialDescriptor = known.descriptor", "",
                        f"    override fun deserialize(decoder: Decoder): {name} {{",
                        f"        val input = decoder as? JsonDecoder ?: throw SerializationException(\"{name} needs JSON\")",
                        "        val obj = input.decodeJsonElement() as? JsonObject",
                        f"            ?: throw SerializationException(\"{name} needs an object\")",
                        "        val fields = input.json.decodeFromJsonElement(known, "
                        + ("JsonObject(obj.filterKeys { it in keys })" if d.extra else "obj") + ")",
                        f"        return {name}("]
                member_names = [kt_ident(lower_camel(f.name)) for f in d.fields]
                if d.union:
                    member_names.insert(0, kt_ident(lower_camel(defs[d.union].discriminator)))
                for n in member_names:
                    out.append(f"            {n} = fields.{n},")
                if d.extra:
                    out.append("            additionalProperties = obj.filterKeys { it !in keys },")
                out += ["        )", "    }", "",
                        f"    override fun serialize(encoder: Encoder, value: {name}) {{",
                        f"        val output = encoder as? JsonEncoder ?: throw SerializationException(\"{name} needs JSON\")",
                        f"        val fields = {known_name}("]
                for n in member_names:
                    out.append(f"            {n} = value.{n},")
                out.append("        )")
                codec = "output.json"
                if d.preserve_required:
                    out.append("        val codec = Json(output.json) { encodeDefaults = true; explicitNulls = false }")
                    codec = "codec"
                out.append(f"        val obj = {codec}.encodeToJsonElement(known, fields) as JsonObject")
                if d.extra:
                    out.append("        output.encodeJsonElement(JsonObject(value.additionalProperties.filterKeys { it !in keys } + obj))")
                else:
                    out.append("        output.encodeJsonElement(obj)")
                out += ["    }", "}", ""]
        elif d.kind == "union":
            name, disc = kname(d.name), d.discriminator
            dk = kt_ident(lower_camel(disc))
            out += kdoc(d.doc)
            out += [f"@Serializable(with = {name}Serializer::class)", f"sealed interface {name} {{",
                    f"    val {dk}: String", "",
                    f"    /** {unknown_doc(d)} */",
                    f"    data class Unknown(override val {dk}: String, val raw: JsonObject = JsonObject(emptyMap()))"
                    f" : {name}", "", "    companion object {",
                    f"        val {upper_snake(plural(disc))} = listOf("
                    + ", ".join(json.dumps(defs[v].tag) for v in d.variants) + ")"]
            if any(defs[v].since for v in d.variants):
                out.append("        val SINCE = mapOf("
                           + ", ".join(f"{json.dumps(defs[v].tag)} to {defs[v].since}" for v in d.variants) + ")")
            out += ["    }", "}", "",
                    f"object {name}Serializer : KSerializer<{name}> {{",
                    "    override val descriptor: SerialDescriptor = JsonElement.serializer().descriptor", "",
                    f"    override fun deserialize(decoder: Decoder): {name} {{",
                    f"        val input = decoder as? JsonDecoder ?: throw SerializationException(\"{name} needs JSON\")",
                    "        val obj = input.decodeJsonElement() as? JsonObject",
                    f"            ?: return {name}.Unknown(\"\")",
                    f"        val tag = (obj[\"{disc}\"] as? JsonPrimitive)?.contentOrNull ?: \"\"",
                    "        return try {", "            when (tag) {"]
            for v in d.variants:
                out.append(f"                {json.dumps(defs[v].tag)} -> input.json.decodeFromJsonElement("
                           f"{kname(v)}.serializer(), obj)")
            out += [f"                else -> {name}.Unknown(tag, obj)", "            }",
                    "        } catch (_: SerializationException) {",
                    f"            {name}.Unknown(tag, obj)",
                    "        } catch (_: IllegalArgumentException) {",
                    f"            {name}.Unknown(tag, obj)", "        }", "    }", "",
                    f"    override fun serialize(encoder: Encoder, value: {name}) {{",
                    f"        val output = encoder as? JsonEncoder ?: throw SerializationException(\"{name} needs JSON\")",
                    "        val obj = when (value) {"]
            for v in d.variants:
                out.append(f"            is {kname(v)} -> output.json.encodeToJsonElement("
                           f"{kname(v)}.serializer(), value) as JsonObject")
            out += [f"            is {name}.Unknown -> value.raw", "        }",
                    f"        output.encodeJsonElement(JsonObject(obj + (\"{disc}\" to JsonPrimitive(value.{dk}))))",
                    "    }", "}", ""]
    # Imports only some definitions use: kept when the code names them.
    code = "\n".join(line for line in out if not line.startswith("import "))
    return "\n".join(line for line in out if not line.startswith("import ")
                     or re.search(r"\b" + re.escape(line.rsplit(".", 1)[1]) + r"\b", code))
