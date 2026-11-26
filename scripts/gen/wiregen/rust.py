"""Rust: serde types for a schema/wire file whose x-out names "rust" (#15397). Structs derive Serialize and Deserialize;
a closed enum, an ID and a union write their own impls so that an unknown value or variant decodes to an explicit
Unknown carrying what came. Depends on serde and serde_json only; times stay RFC 3339 strings."""

from __future__ import annotations

import json
import re

from .model import DEFAULT_HEADER, Def, Field, SchemaError, TypeRef, reserved_keys, unknown_doc
from .names import camel, comment, plural, sentence, upper_snake

RUST_KEYWORDS = {"as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern", "false",
                 "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return",
                 "static", "struct", "super", "trait", "true", "type", "unsafe", "use", "where", "while", "abstract",
                 "become", "box", "do", "final", "macro", "override", "priv", "typeof", "unsized", "virtual", "yield",
                 "try", "gen"}
VALUE = "serde_json::Value"
MAP = "std::collections::BTreeMap"


def snake(s: str) -> str:
    s = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", s)
    return re.sub(r"[^A-Za-z0-9]+", "_", s).strip("_").lower()


def rust_field(s: str) -> str:
    n = snake(s)
    return f"r#{n}" if n in RUST_KEYWORDS else n


def rust_variant(value: str) -> str:
    """A closed enum's or a union's variant: UpperCamel, with a suffix where it would be the type's own Unknown."""
    n = camel(value)
    return n + "Value" if n == "Unknown" else n


def gen_rust(defs: dict[str, Def], *, header: list[str] | None = None, defects: str = "ExtensionWire") -> str:
    """Rust for one schema. defects names the static hook closed enums with x-unknown "count" call
    (<DEFECTS>_UNKNOWN_ENUM)."""
    hook = upper_snake(defects) + "_UNKNOWN_ENUM"
    out: list[str] = []

    def rtype(t: TypeRef) -> str:
        k = t.kind
        if k in ("string", "time"):
            return "String"
        if k in ("int", "int64"):
            return "i64"
        if k == "number":
            return "f64"
        if k == "bool":
            return "bool"
        if k in ("any", "null"):
            return VALUE
        if k == "array":
            return f"Vec<{rtype(t.of)}>"
        if k == "map":
            return f"{MAP}<String, {rtype(t.of)}>"
        d = defs[t.ref]
        return "String" if d.kind == "enum" and not d.closed else t.ref

    def doc(text: str, indent: str = "") -> list[str]:
        return comment(f"{indent}/// ", text)

    def field_lines(d: Def, f: Field) -> list[str]:
        if f.required and f.type.kind == "ref" and defs[f.type.ref].kind == "union":
            raise SchemaError(f"{d.name}.{f.name}: a required field of union type {f.type.ref}: not in the subset")
        n = rust_field(f.name)
        attrs = []
        if n.removeprefix("r#") != f.name:
            attrs.append(f"rename = {json.dumps(f.name)}")
        typ = rtype(f.type)
        if not f.required:
            typ = f"Option<{typ}>"
            attrs += ["default", 'skip_serializing_if = "Option::is_none"']
        elif not (d.strict or d.preserve_required):
            # A missing required field takes its zero value, as Swift's and Kotlin's permissive decoders do; with
            # x-decode "strict" it fails to decode.
            attrs.append("default")
        lines = doc(f.doc, "    ")
        if attrs:
            lines.append(f"    #[serde({', '.join(attrs)})]")
        return lines + [f"    pub {n}: {typ},"]

    def struct(d: Def) -> list[str]:
        lines = doc(d.doc)
        lines += ["#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize)]", f"pub struct {d.name} {{"]
        for f in d.fields:
            lines += field_lines(d, f)
        if d.extra:
            lines += ["    /// Extra keys stay at this object's level on the wire; declared fields always win.",
                      "    #[serde(flatten)]", f"    pub additional_properties: {MAP}<String, {VALUE}>,"]
        return lines + ["}", ""]

    def closed_enum(d: Def) -> list[str]:
        cases = [(v, rust_variant(v)) for v in d.values]
        lines = doc((d.doc or f"{d.name}: a closed set of values.") + " A value this side does not know decodes to "
                    "Unknown with the raw value, and encodes back as it came.")
        lines += ["#[derive(Debug, Clone, PartialEq, Eq, Hash)]", f"pub enum {d.name} {{"]
        lines += [f"    {c}," for _, c in cases]
        lines += ["    /// A value this side does not know, kept as it came.", "    Unknown(String),", "}", "",
                  f"impl {d.name} {{", f"    /// The values of {d.name}, in order.",
                  f"    pub const ALL: &'static [{d.name}] = &[{', '.join(f'{d.name}::{c}' for _, c in cases)}];", "",
                  "    /// A raw value: one this side knows, or Unknown with it.",
                  "    pub fn parse(raw: &str) -> Self {", "        match raw {"]
        lines += [f"            {json.dumps(v)} => {d.name}::{c}," for v, c in cases]
        lines += [f"            _ => {d.name}::Unknown(raw.to_owned()),", "        }", "    }", "",
                  "    /// The value as it is on the wire.", "    pub fn as_str(&self) -> &str {", "        match self {"]
        lines += [f"            {d.name}::{c} => {json.dumps(v)}," for v, c in cases]
        lines += [f"            {d.name}::Unknown(raw) => raw,", "        }", "    }", "}", "",
                  f"impl Default for {d.name} {{", "    fn default() -> Self {",
                  f"        {d.name}::Unknown(String::new())", "    }", "}", "",
                  f"impl Serialize for {d.name} {{",
                  "    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {",
                  "        serializer.serialize_str(self.as_str())", "    }", "}", "",
                  f"impl<'de> Deserialize<'de> for {d.name} {{",
                  "    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {",
                  "        let raw = String::deserialize(deserializer)?;", f"        let value = {d.name}::parse(&raw);"]
        if d.count_unknown:
            lines += [f"        if let ({d.name}::Unknown(_), Some(hook)) = (&value, {hook}.get()) {{",
                      f"            hook({json.dumps(d.name)}, &raw);", "        }"]
        lines += ["        Ok(value)", "    }", "}", ""]
        return lines

    def id_type(d: Def) -> list[str]:
        what = "a label (free text, not a closed set)" if d.kind == "text" else "an ID"
        lines = doc((d.doc or f"{d.name}: {what}.") + " It encodes as the bare string.")
        lines += ["#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord, Default, Serialize, Deserialize)]",
                  "#[serde(transparent)]", f"pub struct {d.name}(pub String);", "",
                  f"impl {d.name} {{", "    pub fn new(raw: impl Into<String>) -> Self {", "        Self(raw.into())",
                  "    }", "", "    pub fn as_str(&self) -> &str {", "        &self.0", "    }", "}", "",
                  f"impl std::fmt::Display for {d.name} {{",
                  "    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {", "        f.write_str(&self.0)",
                  "    }", "}", ""]
        return lines

    def union(d: Def) -> list[str]:
        disc, tag_field = d.discriminator, rust_field(d.discriminator)
        cases = [(defs[v].tag, rust_variant(defs[v].tag), v) for v in d.variants]
        lines = doc(d.doc)
        lines += ["#[derive(Debug, Clone, PartialEq)]", f"pub enum {d.name} {{"]
        lines += [f"    {c}({v})," for _, c, v in cases]
        lines += doc(unknown_doc(d), "    ")
        lines += [f"    Unknown {{ {tag_field}: String, raw: {VALUE} }},", "}", "",
                  f"impl {d.name} {{", f"    /// The {plural(disc)} of {d.name}, in order.",
                  f"    pub const {upper_snake(plural(disc))}: &'static [&'static str] = "
                  f"&[{', '.join(json.dumps(t) for t, _, _ in cases)}];", "",
                  f"    /// The {disc} of this value.", f"    pub fn {tag_field}(&self) -> &str {{", "        match self {"]
        lines += [f"            {d.name}::{c}(_) => {json.dumps(t)}," for t, c, _ in cases]
        lines += [f"            {d.name}::Unknown {{ {tag_field}, .. }} => {tag_field},", "        }", "    }", "}", "",
                  f"impl Serialize for {d.name} {{",
                  "    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {",
                  "        let mut value = match self {"]
        lines += [f"            {d.name}::{c}(v) => serde_json::to_value(v).map_err(serde::ser::Error::custom)?,"
                  for _, c, _ in cases]
        lines += [f"            {d.name}::Unknown {{ raw, .. }} => raw.clone(),", "        };",
                  f"        if let {VALUE}::Object(fields) = &mut value {{",
                  f"            fields.insert({json.dumps(disc)}.to_owned(), {VALUE}::String(self.{tag_field}().to_owned()));",
                  "        }", "        value.serialize(serializer)", "    }", "}", "",
                  f"impl<'de> Deserialize<'de> for {d.name} {{",
                  "    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {",
                  f"        let raw = {VALUE}::deserialize(deserializer)?;",
                  f"        let {tag_field} = raw.get({json.dumps(disc)}).and_then({VALUE}::as_str).unwrap_or(\"\").to_owned();",
                  f"        let known = match {tag_field}.as_str() {{"]
        for t, c, v in cases:
            if defs[v].extra:
                keys = ", ".join(json.dumps(k) for k in reserved_keys(defs, defs[v]))
                lines.append(f"            {json.dumps(t)} => serde_json::from_value::<{v}>(raw.clone()).ok().map(|mut v| {{"
                             f" for key in [{keys}] {{ v.additional_properties.remove(key); }} {d.name}::{c}(v) }}),")
            else:
                lines.append(f"            {json.dumps(t)} => serde_json::from_value(raw.clone()).ok().map({d.name}::{c}),")
        lines += ["            _ => None,", "        };",
                  f"        Ok(known.unwrap_or({d.name}::Unknown {{ {tag_field}, raw }}))", "    }", "}", ""]
        return lines

    if any(d.count_unknown for d in defs.values()):
        out += doc("Called with a closed enum's name and the raw value each time one with x-unknown \"count\" decodes "
                   "a value this side does not know. Unset: nothing is called. Set it once, before decoding starts.")
        out += [f"pub static {hook}: std::sync::OnceLock<fn(&str, &str)> = std::sync::OnceLock::new();", ""]
    for d in defs.values():
        if d.external:
            raise SchemaError(f"{d.name} of {d.external}: Rust does not take a shared file's IDs yet")
        if d.kind == "enum" and d.closed:
            out += closed_enum(d)
        elif d.kind == "enum":
            out += doc(d.doc or f"Values of {d.name}.")
            out += doc("An open set: a value a newer side sends passes through as it came.")
            out += [f"pub type {d.name} = String;", "", f"/// The values of {d.name}, in order.",
                    f"pub const {upper_snake(plural(d.name))}: &[&str] = &[{', '.join(json.dumps(v) for v in d.values)}];",
                    ""]
        elif d.kind in ("id", "text"):
            out += id_type(d)
        elif d.kind == "opaque":
            out += doc(" ".join(x for x in (d.doc, f"Opaque JSON: {sentence(d.reason)}") if x))
            out += [f"pub type {d.name} = {VALUE};", ""]
        elif d.kind == "any":
            out += doc(d.doc) + [f"pub type {d.name} = {VALUE};", ""]
        elif d.kind == "struct":
            out += struct(d)
        elif d.kind == "union":
            out += union(d)
    code = "\n".join(out)
    uses = [t for t in ("Deserialize", "Serialize") if re.search(r"\b" + t + r"\b", code)]
    head = [*(header or DEFAULT_HEADER), ""]
    if uses:
        head += [f"use serde::{{{', '.join(uses)}}};" if len(uses) > 1 else f"use serde::{uses[0]};", ""]
    return "\n".join(head + out)
