#!/usr/bin/env python3
"""Start a server domain package that follows the domain rules from its first line.

    python3 scripts/new_domain_slice.py go <dir under server/internal> --doc "<one sentence>" [flags]

The rules are docs/server/architecture.md "Domain model"; the package this writes passes them (its rules_test.go hook
runs them in its own go test), so the compliant shape is the cheapest one to start from: value objects, closed sets
with visitors, the package's failure family, an entity with a staged builder, a decider and an adapter come written,
with black-box tests. Where a type's zero is no value, its constructors refuse it and every function handed it raises
a defect, and the tests pin both. Edit what it writes into the domain's own words; `go test ./<dir>/...` names each
rule a change breaks and its fix. It refuses a directory that exists.

What each flag writes (Go):

  --doc "decides ..."        doc.go: "Package <name> decides ..." and //persona:layer core
  --shared                   //persona:layer core shared: a shared kernel directly under server/internal, values and
                             closed sets only (no --entity, --builder or --decider)
  --value Name=string|int    values.go: a value object with one field named for the type, ParseName (an empty text,
                             or a count of zero or less, is invalid) and Wire
    --zero-is "<meaning>"    after a --value: its zero is a real value (no text, no count) that ParseName returns
  --value Name=part:Type,... a value of parts, NewName (a zero part is invalid) and Accept(NameVisitor)
  --parse Name=raw:string,... a value parsed from several raw inputs, each named for the field of a --value of that
                             kind (--value MachineID=string --parse Ref=machine:string,...); ParseName reports every
                             invalid part
  --closed Name=a,b,c        closed.go: a closed set over its own field, NameA() ... and Accept(NameVisitor)
  --closed Name=a,b(Type)    a sealed interface over unexported variants; NameB(payload) (name the payload with
                             b(name:Type))
  --entity Name --id NameID  entity.go: an entity holding the --value types no --parse or part uses and the closed
                             sets without payloads (one at least, unless --decider gives it a lifecycle); NewName,
                             RestoreName, ID, IsSame and Accept(NameVisitor)
  --builder                  a staged builder (NewNameBuilder, one stage per part, Build refusing every zero part)
  --decider                  decide.go: an open / closed lifecycle with sealed Command and Event, Decide, Decided.Then
                             and Accept, and an unexported evolve; rename it to the domain's own
  --adapter                  <dir>/<name>adapt: Record, Restore, Store, Log, its hook and a fixture replay test
  --act                      with --adapter and --decider: Act(prev, decided, save)

Types a part or a payload may name: the package's own values, closed sets and entity id, instant.Instant,
instant.Span, slot.Slot, and agentref.Ref, agentref.MachineID or agentref.InteractionID.

The Swift, Kotlin and web flavours (`swift <Ctx>`, `kotlin <ctx>`, `web <ctx>`) arrive with each platform's first
domain module (#10613: items 6.5, 6.11 and 6.1); only Go is written today.
"""
import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import textwrap

ROOT = Path(__file__).resolve().parents[1]
SERVER = ROOT / "server"

KIT = "internal/domainkit/"
AGENTREF = "internal/agentref"
# The kit's and the shared kernel's types a part or payload may name: (import dir, zero is invalid, default name).
FOREIGN = {
    "instant.Instant": (KIT + "instant", True, "at"),
    "instant.Span": (KIT + "instant", False, "span"),
    "slot.Slot": (KIT + "slot", False, "place"),
    "agentref.Ref": (AGENTREF, True, "ref"),
    "agentref.MachineID": (AGENTREF, True, "machine"),
    "agentref.InteractionID": (AGENTREF, True, "interaction"),
}
# How a test makes a valid value of a foreign type.
FOREIGN_SAMPLE = {
    "instant.Instant": "at",
    "instant.Span": "instant.ParseMinutes(5)",
    "slot.Slot": "first",
    "agentref.Ref": 'domaintest.Ok(t, agentref.ParseRef("machine 1", "session 1"))',
    "agentref.MachineID": 'domaintest.Ok(t, agentref.ParseMachineID("machine 1"))',
    "agentref.InteractionID": 'domaintest.Ok(t, agentref.ParseInteractionID("card 1"))',
}
LATER = {"swift": "6.5", "kotlin": "6.11", "web": "6.1"}
GO_KEYWORDS = {"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func",
               "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct",
               "switch", "type", "var"}
WIDTH = 120


class Refused(Exception):
    """A request the scaffold turns down, with what to do instead."""


# Names.

def words(name):
    """The words of a Go name: HouseID -> [house, id], URLPath -> [url, path]."""
    return [w.lower() for w in re.findall(r"[A-Z]+(?=[A-Z][a-z]|\d|\b)|[A-Z]?[a-z]+|[A-Z]+|\d+", name)]


def lower_camel(name):
    """name with its leading capitals lowered, as the rules spell a field named for a type (MachineID -> machineID)."""
    i = 0
    while i < len(name) and name[i].isupper():
        i += 1
    if i == 0:
        return name
    if i == 1 or i == len(name):
        return name[:i].lower() + name[i:]
    return name[:i - 1].lower() + name[i - 1:]


def field_of(name):
    """The one field of a value named for its type: MachineID -> machine, SearchText -> searchText."""
    if name.endswith("ID") and len(name) > 2:
        return lower_camel(name[:-2])
    return lower_camel(name)


def snake(name):
    return "_".join(words(name))


def camel(word):
    """A snake_case word as an exported Go name: needs_you -> NeedsYou."""
    return "".join(part[:1].upper() + part[1:] for part in word.split("_"))


def spoken(name):
    return " ".join(words(name))


def plural(word):
    """word for more than one: chore -> chores, search -> searches."""
    return word + ("es" if re.search(r"(s|x|z|ch|sh)$", word) else "s")


def article(phrase):
    return ("an " if phrase[:1] in "aeiou" else "a ") + phrase


def exported(name):
    return re.fullmatch(r"[A-Z][A-Za-z0-9]*", name) is not None


def comment(text, indent=""):
    """text as // lines no wider than WIDTH. "The zero X is" stays on one line: the rules look for it whole."""
    keep = re.sub(r"The zero \w+ is", lambda m: m.group(0).replace(" ", "\u00a0"), text)
    lines = textwrap.wrap(keep, WIDTH - len(indent) - 3, break_long_words=False, break_on_hyphens=False)
    return "".join(f"{indent}// {line.replace(chr(0xa0), ' ')}\n" for line in lines)


# The request.

class Value:
    def __init__(self, name, kind, parts=(), zero_is=None):
        self.name, self.kind, self.parts, self.zero_is = name, kind, list(parts), zero_is
        self.field = field_of(name)

    @property
    def simple(self):
        return self.kind in ("string", "int")


class Parsed:
    def __init__(self, name, raws):
        self.name, self.raws = name, raws  # raws: [(raw name, kind, Value)]


class Closed:
    def __init__(self, name, cases):
        self.name, self.cases = name, cases  # cases: [(word, payload name or None, payload type or None)]
        self.field = field_of(name)

    @property
    def plain(self):
        return all(t is None for _, _, t in self.cases)


class Slice:
    """Everything the flags ask for, checked."""

    def __init__(self, args, module):
        self.module = module
        self.dir = args.dir.strip("/")
        if not re.fullmatch(r"internal(/[a-z][a-z0-9]*)+", self.dir):
            raise Refused(f"{args.dir}: the directory is under server/internal, in lowercase path elements "
                          "(internal/assistant/<name>)")
        self.name = self.dir.rsplit("/", 1)[1]
        if self.name in GO_KEYWORDS:
            raise Refused(f"{self.name} is a Go keyword; name the package otherwise")
        self.doc = " ".join(args.doc.split())
        if not self.doc or self.doc.startswith("Package "):
            raise Refused('--doc is one sentence that follows "Package <name> " (--doc "decides ...")')
        if not self.doc.endswith("."):
            self.doc += "."
        self.shared = args.shared
        if self.shared:
            if self.dir.count("/") != 1:
                raise Refused("a shared kernel (--shared) lives directly under server/internal (internal/<name>)")
            for flag, on in (("--entity", args.entity), ("--builder", args.builder), ("--decider", args.decider)):
                if on:
                    raise Refused(f"a shared kernel holds values and closed sets only: {flag} makes it an entity's "
                                  "package; drop --shared and keep the package in its context")
        self.values, self.parsed, self.closed = [], [], []
        self.names = {}
        for i, spec in enumerate(args.value or []):
            self.values.append(self.value(spec, (args.zero_is or {}).get(i)))
        self.entity = args.entity
        self.id = args.id
        if self.entity:
            self.declare(self.entity, "--entity")
            if not self.id or not self.id.endswith("ID") or not exported(self.id) or len(self.id) < 3:
                raise Refused("--entity takes --id <Name>ID, the type its identity is (--entity Chore --id ChoreID)")
            if self.id not in self.names:
                self.values.append(Value(self.id, "string"))
                self.declare(self.id, "--id")
            elif not isinstance(self.local(self.id), Value) or self.local(self.id).kind != "string":
                raise Refused(f"--id {self.id} names a --value of string, or one --entity adds")
            elif self.local(self.id).zero_is is not None:
                raise Refused(f"--id {self.id}: the zero of an identity is no {spoken(self.entity)}, so it takes no "
                              "--zero-is")
        elif self.id:
            raise Refused("--id goes with --entity")
        for spec in args.parse or []:
            self.parsed.append(self.parse_spec(spec))
        for spec in args.closed or []:
            self.closed.append(self.closed_spec(spec))
        for v in self.values:
            for _, t in v.parts:
                self.check_type(t, f"--value {v.name}")
        self.acyclic()
        for c in self.closed:
            for _, _, t in c.cases:
                if t:
                    self.check_type(t, f"--closed {c.name}")
        self.builder, self.decider, self.adapter, self.act = args.builder, args.decider, args.adapter, args.act
        if (self.builder or self.decider) and not self.entity:
            raise Refused("--builder and --decider build an entity: add --entity <Name> --id <Name>ID")
        if self.act and not (self.adapter and self.decider):
            raise Refused("--act acts on a decider's decisions in the adapter: add --adapter and --decider")
        if not (self.values or self.closed):
            raise Refused("a slice holds at least one --value, --closed or --entity")
        used = {raw[2].name for p in self.parsed for raw in p.raws}
        used |= {t for v in self.values for _, t in v.parts}
        self.parts = []  # the entity's parts besides its id, in flag order
        if self.entity:
            self.parts = [v for v in self.values if v.simple and v.name != self.id and v.name not in used]
            self.parts += [c for c in self.closed if c.plain]
            if not self.parts and not self.decider:
                raise Refused(f"--entity {self.entity} holds only its id, which makes it a value: give it a part (a "
                              "--value of string or int no other value or --parse uses, or a --closed set without "
                              "payloads) or a lifecycle (--decider)")
            given = 1 + len(self.parts) + (1 if self.decider else 0)
            if not self.builder and given > 4:
                raise Refused(f"{self.entity} has {given} inputs to New or Restore, and a function takes four at most: "
                              "add --builder")
        if self.adapter:
            if self.entity:
                self.primary = self.entity
            elif self.parsed:
                self.primary = self.parsed[0]
            elif any(v.simple for v in self.values):
                self.primary = next(v for v in self.values if v.simple)
            else:
                raise Refused("--adapter stores an --entity, a --parse value or a --value Name=string|int")
        self.reserve()

    def declare(self, name, flag):
        if not exported(name):
            raise Refused(f"{flag} {name}: a type name is an exported Go name (Title, HouseID)")
        if name in self.names:
            raise Refused(f"{name} is declared twice")
        self.names[name] = flag

    def reserve(self):
        """The names the scaffold writes itself, which no type may take."""
        taken = {"Fail", "Visitor"}
        for v in self.values:
            if not v.simple:
                taken.add(v.name + "Visitor")
        for p in self.parsed:
            taken.add(p.name + "Visitor")
        for c in self.closed:
            taken.add(c.name + "Visitor")
        if self.entity:
            taken |= {self.entity + "Visitor", self.entity + "Builder"}
            taken |= {self.entity + p.name + "Step" for p in self.parts}
            taken.add("Known")  # a part's field, known, would clash with the entity's known check
        if self.decider:
            taken |= {"Command", "Event", "EventVisitor", "Decided", "DecidedVisitor"}
            taken |= {"Closed", "Events"}  # a field named closed or events would clash with the entity's or Decided's
        clash = sorted(taken & set(self.names))
        if clash:
            raise Refused(f"{', '.join(clash)}: the scaffold declares that name itself; name the type otherwise")

    def value(self, spec, zero_is):
        name, _, kind = spec.partition("=")
        self.declare(name, "--value")
        if kind in ("string", "int"):
            return Value(name, kind, zero_is=zero_is)
        if zero_is is not None:
            raise Refused(f"--zero-is follows a --value Name=string|int; {name} is made of parts, and its zero is "
                          "never a real value")
        parts = []
        for item in kind.split(","):
            part, _, t = item.partition(":")
            if not re.fullmatch(r"[a-z][A-Za-z0-9]*", part) or not t:
                raise Refused(f"--value {spec}: parts are part:Type, part a lowerCamel name (opens:instant.Instant)")
            parts.append((part, t))
        if not parts or len(parts) > 4 or len({p for p, _ in parts}) != len(parts):
            raise Refused(f"--value {spec}: one to four parts with distinct names (a function takes four at most)")
        return Value(name, "parts", parts)

    def parse_spec(self, spec):
        name, _, raws = spec.partition("=")
        self.declare(name, "--parse")
        out = []
        for item in raws.split(","):
            raw, _, kind = item.partition(":")
            match = [v for v in self.values if v.simple and v.field == raw and v.kind == kind]
            if not match:
                raise Refused(f"--parse {spec}: {raw}:{kind} needs a --value of {kind} whose field is {raw} "
                              f"(--value {camel(raw)}ID={kind} or --value {camel(raw)}={kind})")
            out.append((raw, kind, match[0]))
        if len(out) < 2 or len(out) > 4:
            raise Refused(f"--parse {spec}: two to four raw inputs (one is a --value)")
        return Parsed(name, out)

    def closed_spec(self, spec):
        name, _, body = spec.partition("=")
        self.declare(name, "--closed")
        cases = []
        for item in re.findall(r"[^,(]+(?:\([^)]*\))?", body):
            m = re.fullmatch(r"([a-z][a-z0-9_]*)(?:\((?:([a-z][A-Za-z0-9]*):)?([A-Za-z][A-Za-z0-9.]*)\))?", item.strip())
            if not m:
                raise Refused(f"--closed {spec}: values are lowercase words (needs_you), a payload in parentheses "
                              "(ring(instant.Instant) or ring(at:instant.Instant))")
            word, pname, ptype = m.groups()
            if ptype and not pname:
                pname = FOREIGN[ptype][2] if ptype in FOREIGN else field_of(ptype)
            cases.append((word, pname, ptype))
        if len(cases) < 2 or len({camel(w) for w, _, _ in cases}) != len(cases):
            raise Refused(f"--closed {spec}: two or more distinct values")
        return Closed(name, cases)

    def check_type(self, t, where):
        if t in FOREIGN:
            return
        if t in self.names and t != self.entity:
            return
        raise Refused(f"{where}: {t} is none of this package's values, closed sets or ids, nor "
                      f"{', '.join(sorted(FOREIGN))}")

    # What the code needs to know about a type.

    def local(self, t):
        for v in self.values:
            if v.name == t:
                return v
        for p in self.parsed:
            if p.name == t:
                return p
        for c in self.closed:
            if c.name == t:
                return c
        return None

    def zero_invalid(self, t):
        """Whether a type's zero value is no value, which constructors refuse and functions raise a defect on: a
        simple value's unless --zero-is says what it is, a value of parts or parsed parts when one part's is, and
        every closed set's (no constructor makes a plain set's zero, and a payload set's is nil)."""
        if t in FOREIGN:
            return FOREIGN[t][1]
        x = self.local(t)
        if isinstance(x, Value):
            return x.zero_is is None if x.simple else any(self.zero_invalid(pt) for _, pt in x.parts)
        if isinstance(x, Parsed):
            return any(v.zero_is is None for _, _, v in x.raws)
        return True

    def acyclic(self):
        """Refuses a value that holds itself through its parts: Go has no struct that contains itself."""
        def visit(v, path):
            for _, t in v.parts:
                x = self.local(t)
                if x is v or x in path:
                    raise Refused(f"--value {path[0].name}: its parts hold {v.name} again "
                                  f"({' -> '.join(y.name for y in path + [x])}); a value holds other values")
                if isinstance(x, Value) and not x.simple:
                    visit(x, path + [x])
        for v in self.values:
            if not v.simple:
                visit(v, [v])

    def imports_for(self, t):
        return {self.module + "/" + FOREIGN[t][0]} if t in FOREIGN else set()


# Go source.

def imports(paths):
    std = sorted(p for p in paths if "." not in p.split("/")[0])
    other = sorted(p for p in paths if "." in p.split("/")[0])
    if not std and not other:
        return ""
    if len(std) + len(other) == 1:
        return f'import "{(std + other)[0]}"\n\n'
    groups = [g for g in (std, other) if g]
    body = "\n".join("".join(f'\t"{p}"\n' for p in g) for g in groups)
    return f"import (\n{body})\n\n"


class Source:
    """One Go file: its imports, gathered as code asks for them, and its body."""

    def __init__(self, package):
        self.package, self.paths, self.parts = package, set(), []

    def need(self, *paths):
        self.paths.update(paths)

    def add(self, text):
        self.parts.append(text)

    def text(self):
        body = "\n".join(self.parts)
        code = re.sub(r"//[^\n]*", "", body)  # a package named in a comment is no use of it
        used = {path for path in self.paths if re.search(r"\b" + re.escape(path.rsplit("/", 1)[-1]) + r"\.", code)}
        return f"package {self.package}\n\n" + imports(used) + body


def receiver(name):
    """A receiver's name: the type's first letter, unless the code around it uses that letter (t, v, f, b, d, e, s, r)."""
    r = name[0].lower()
    return "x" if r in ("t", "v", "f", "b", "d", "e", "s", "r") else r


class Writer:
    def __init__(self, s):
        self.s = s
        self.kit = lambda pkg: s.module + "/" + KIT + pkg
        self.used_wrong = False

    def files(self):
        s = self.s
        files = {}
        files["doc.go"] = (comment(f"Package {s.name} {s.doc}") + "//\n" +
                           f"//persona:layer {'core shared' if s.shared else 'core'}\npackage {s.name}\n")
        if s.values or s.parsed:
            files["values.go"] = self.values_go()
        if s.closed:
            files["closed.go"] = self.closed_go()
        if s.entity:
            files["entity.go"] = self.entity_go()
        if s.decider:
            files["decide.go"] = self.decide_go()
        files["fail.go"] = self.fail_go()
        files["rules_test.go"] = rules_test(s.name, s.module)
        files[f"{s.name}_test.go"] = Tests(s).text()
        if s.adapter:
            for name, text in Adapter(s).files().items():
                files[f"{s.name}adapt/{name}"] = text
        return files

    # fail.go

    def fail_go(self):
        s = self.s
        src = Source(s.name)
        src.need(self.kit("fail"))
        src.add(comment(f"Fail is every failure {s.name} returns. The zero Fail is nil; no operation returns it as a "
                        "failure.") +
                f"type Fail interface {{\n\tfail.Fail\n\t{s.name}Fail()\n}}\n")
        if self.used_wrong:
            src.add(comment(f"invalid is input {s.name} refuses: every part that broke a rule, in order.") +
                    f"type invalid struct{{ fail.Invalids }}\n\nfunc (invalid) {s.name}Fail() {{}}\n")
        if s.decider:
            src.add(comment(f"refused is a command the state of {article(spoken(s.entity))} turns down.") +
                    f"type refused struct{{ fail.Refusal }}\n\nfunc (refused) {s.name}Fail() {{}}\n")
        if not self.used_wrong:
            return src.text()
        src.add(comment("wrong adds the part to parts unless ok holds: each rule adds its own, so a caller reports "
                        "every wrong part at once. A part names the input in this package's words, never a wire "
                        "field's.") +
                "func wrong(parts []fail.Invalid, ok bool, code, part string) []fail.Invalid {\n"
                "\tif ok {\n\t\treturn parts\n\t}\n"
                "\treturn append(parts, fail.NewInvalid(fail.NewCode(code), fail.NewPart(part)))\n}\n")
        src.add(comment("invalidParts is the failure for the parts a rule refused; parts is never empty.") +
                "func invalidParts(parts []fail.Invalid) Fail {\n"
                "\tall := fail.NewInvalids(parts[0])\n"
                "\tfor _, p := range parts[1:] {\n\t\tall = all.With(p)\n\t}\n"
                "\treturn invalid{all}\n}\n")
        return src.text()

    # values.go

    def values_go(self):
        s = self.s
        src = Source(s.name)
        src.need(self.kit("defect"))
        for v in s.values:
            if v.simple:
                self.simple_value(src, v)
            else:
                self.parts_value(src, v)
        for p in s.parsed:
            self.parsed_value(src, p)
        return src.text()

    def rule(self, v):
        """The check a simple value's raw input passes, as (Go condition on raw, code word, what the doc says), or
        None for a zero-is text, which every raw is."""
        if v.kind == "string":
            if v.zero_is is not None:
                return None
            return "strings.TrimSpace(raw) != \"\"", "empty", "empty text is invalid"
        if v.zero_is is not None:
            return "raw >= 0", "negative", f"less than zero is invalid, and 0 is the zero {v.name}"
        return "raw > 0", "not_positive", "zero or less is invalid"

    def simple_value(self, src, v):
        s, n, f, r = self.s, v.name, v.field, receiver(v.name)
        thing = spoken(n)
        if v.zero_is is not None:
            raw0 = "empty text" if v.kind == "string" else "0"
            zero = f"The zero {n} is {v.zero_is}: Parse{n} returns it for {raw0}."
        else:
            zero = f"The zero {n} is no {thing}; Parse{n} never makes one."
        src.add(comment(f"{n} is {article(thing)} as {s.name} knows it. {zero}") + f"type {n} struct{{ {f} {v.kind} }}\n")
        rule = self.rule(v)
        what = "names" if v.kind == "string" else "counts"
        if rule is None:
            src.add(comment(f"Parse{n} is the {thing} raw names; empty text is the zero {n}.") +
                    f"func Parse{n}(raw string) {n} {{ return {n}{{{f}: raw}} }}\n")
        else:
            cond, word, says = rule
            if v.kind == "string":
                src.need("strings")
            src.need(self.kit("outcome"), self.kit("fail"))
            self.used_wrong = True
            src.add(comment(f"Parse{n} is the {thing} raw {what}; {says}.") +
                    f"func Parse{n}(raw {v.kind}) (out outcome.Of[{n}, Fail]) {{\n"
                    f"\tif parts := {lower_camel(n)}Rule(nil, raw); len(parts) > 0 {{\n"
                    "\t\treturn out.Err(invalidParts(parts))\n\t}\n"
                    f"\treturn out.Ok({n}{{{f}: raw}})\n}}\n")
            src.add(comment(f"{lower_camel(n)}Rule adds what is wrong with raw as {article(thing)} to parts.") +
                    f"func {lower_camel(n)}Rule(parts []fail.Invalid, raw {v.kind}) []fail.Invalid {{\n"
                    f'\treturn wrong(parts, {cond}, "{s.name}.{snake(f)}.{word}", "{f}")\n}}\n')
        src.add(comment(f"Wire is the {thing} as the wire carries it. Adapters only.") +
                f"func ({r} {n}) Wire() {v.kind} {{ return {r}.{f} }}\n")

    def parts_value(self, src, v):
        s, n, r = self.s, v.name, receiver(v.name)
        thing = spoken(n)
        names = and_list([f"its {spoken(p)}" for p, _ in v.parts])
        width = max(len(p) for p, _ in v.parts)
        fields = "".join(f"\t{p.ljust(width)} {t}\n" for p, t in v.parts)
        for _, t in v.parts:
            src.need(*s.imports_for(t))
        if s.zero_invalid(n):
            zero = f"The zero {n} is no {thing}; New{n} never makes one, and Accept raises a defect on it."
        else:
            zero = f"The zero {n} is the {thing} of zero parts: New{n} returns it for them."
        src.add(comment(f"{n} is {article(thing)} as {s.name} knows it: {names}. {zero}") +
                f"type {n} struct {{\n{fields}}}\n")
        params = ", ".join(f"{p} {t}" for p, t in v.parts)
        src.add(comment(f"{n}Visitor is told the parts of {article(thing)}.") +
                f"type {n}Visitor interface {{\n\tVisit{n}({params})\n}}\n")
        checks = [(p, t) for p, t in v.parts if s.zero_invalid(t)]
        build = f"{n}{{" + ", ".join(f"{p}: {p}" for p, _ in v.parts) + "}"
        if checks:
            src.need(self.kit("outcome"), self.kit("fail"))
            self.used_wrong = True
            body = zero_vars([t for _, t in checks], "\t") + "\tvar parts []fail.Invalid\n"
            for p, t in checks:
                body += f'\tparts = wrong(parts, {p} != {zero_name(t)}, "{s.name}.{snake(n)}.no_{snake(p)}", "{p}")\n'
            body += "\tif len(parts) > 0 {\n\t\treturn out.Err(invalidParts(parts))\n\t}\n"
            body += f"\treturn out.Ok({build})\n"
            src.add(comment(f"New{n} is the {thing} of its parts; a part that is no value is invalid.") +
                    f"func New{n}({params}) (out outcome.Of[{n}, Fail]) {{\n{body}}}\n")
        else:
            src.add(comment(f"New{n} is the {thing} of its parts.") +
                    f"func New{n}({params}) {n} {{ return {build} }}\n")
        args = ", ".join(f"{r}.{p}" for p, _ in v.parts)
        src.add(self.accept(n, r, f"v.Visit{n}({args})", s.zero_invalid(n)))

    def accept(self, n, r, visit, raises):
        """Accept on a value that tells its parts: visit is the call, raises whether the zero n is a defect."""
        thing = spoken(n)
        if not raises:
            return (comment(f"Accept tells v the parts of the {thing}.") +
                    f"func ({r} {n}) Accept(v {n}Visitor) {{ {visit} }}\n")
        return (comment(f"Accept tells v the parts of the {thing}; the zero {n} is a defect.") +
                f"func ({r} {n}) Accept(v {n}Visitor) {{\n\tif {r} == ({n}{{}}) {{\n"
                f'\t\tdefect.Raise("{self.s.name}.{n}.Accept", "the zero {n}")\n\t}}\n\t{visit}\n}}\n')

    def parsed_value(self, src, p):
        s, n, r = self.s, p.name, receiver(p.name)
        thing = spoken(n)
        names = and_list([f"its {spoken(raw)}" for raw, _, _ in p.raws])
        width = max(len(raw) for raw, _, _ in p.raws)
        fields = "".join(f"\t{raw.ljust(width)} {v.name}\n" for raw, _, v in p.raws)
        src.need(self.kit("outcome"), self.kit("fail"))
        self.used_wrong = True
        if s.zero_invalid(n):
            zero = f"The zero {n} is no {thing}; Parse{n} never makes one, and Accept raises a defect on it."
        else:
            zero = f"The zero {n} is the {thing} of empty parts: Parse{n} returns it for them."
        src.add(comment(f"{n} is {article(thing)} as {s.name} knows it: {names}. It is comparable, so it keys maps. "
                        f"{zero}") + f"type {n} struct {{\n{fields}}}\n")
        visit = ", ".join(f"{raw} {v.name}" for raw, _, v in p.raws)
        src.add(comment(f"{n}Visitor is told the parts of {article(thing)}.") +
                f"type {n}Visitor interface {{\n\tVisit{n}({visit})\n}}\n")
        params = ", ".join(f"{raw} {kind}" for raw, kind, _ in p.raws)
        body = "\tvar parts []fail.Invalid\n"
        for raw, _, v in p.raws:
            if self.rule(v) is not None:
                body += f"\tparts = {lower_camel(v.name)}Rule(parts, {raw})\n"
        body += "\tif len(parts) > 0 {\n\t\treturn out.Err(invalidParts(parts))\n\t}\n"
        build = ", ".join(f"{raw}: {v.name}{{{v.field}: {raw}}}" for raw, _, v in p.raws)
        body += f"\treturn out.Ok({n}{{{build}}})\n"
        src.add(comment(f"Parse{n} is the {thing} its raw parts name; it reports every invalid part, in order.") +
                f"func Parse{n}({params}) (out outcome.Of[{n}, Fail]) {{\n{body}}}\n")
        args = ", ".join(f"{r}.{raw}" for raw, _, _ in p.raws)
        src.add(self.accept(n, r, f"v.Visit{n}({args})", s.zero_invalid(n)))

    # closed.go

    def closed_go(self):
        s = self.s
        src = Source(s.name)
        src.need(self.kit("defect"))
        for c in s.closed:
            if c.plain:
                self.plain_closed(src, c)
            else:
                self.payload_closed(src, c)
        return src.text()

    def plain_closed(self, src, c):
        s, n, f, r = self.s, c.name, c.field, receiver(c.name)
        thing = spoken(n)
        listed = or_list([w.replace("_", " ") for w, _, _ in c.cases])
        src.add(comment(f"{n} is {article(thing)} as {s.name} knows it: {listed}. The zero {n} is no {thing}; only "
                        "its constructors make one, and Accept raises a defect on it.") +
                f"type {n} struct{{ {f} uint8 }}\n")
        for i, (word, _, _) in enumerate(c.cases, 1):
            src.add(comment(f"{n}{camel(word)} is the {word.replace('_', ' ')} {thing}.") +
                    f"func {n}{camel(word)}() {n} {{ return {n}{{{f}: {i}}} }}\n")
        visits = "".join(f"\tVisit{camel(w)}()\n" for w, _, _ in c.cases)
        src.add(comment(f"{n}Visitor is told which {thing} it is.") + f"type {n}Visitor interface {{\n{visits}}}\n")
        cases = "".join(f"\tcase {i}:\n\t\tv.Visit{camel(w)}()\n" for i, (w, _, _) in enumerate(c.cases, 1))
        src.add(comment(f"Accept tells v which {thing} it is; the zero {n} is a defect.") +
                f"func ({r} {n}) Accept(v {n}Visitor) {{\n\tswitch {r}.{f} {{\n{cases}\tdefault:\n"
                f'\t\tdefect.Raise("{s.name}.{n}.Accept", "the zero {n}")\n\t}}\n}}\n')

    def payload_closed(self, src, c):
        s, n = self.s, c.name
        thing = spoken(n)
        listed = or_list([w.replace("_", " ") for w, _, _ in c.cases])
        marker = lower_camel(n)
        src.add(comment(f"{n} is {article(thing)} as {s.name} knows it: {listed}. The zero {n} is nil; only its "
                        "constructors make one.") +
                f"type {n} interface {{\n\tAccept(v {n}Visitor)\n\t{marker}()\n}}\n")
        visits = ""
        for w, pname, ptype in c.cases:
            visits += f"\tVisit{camel(w)}({pname} {ptype})\n" if ptype else f"\tVisit{camel(w)}()\n"
            if ptype:
                src.need(*s.imports_for(ptype))
        src.add(comment(f"{n}Visitor is told which {thing} it is, with what it holds.") +
                f"type {n}Visitor interface {{\n{visits}}}\n")
        for w, pname, ptype in c.cases:
            variant = marker + camel(w)
            if ptype:
                src.add(comment(f"{variant} is the {w.replace('_', ' ')} {thing}, with its {spoken(pname)}.") +
                        f"type {variant} struct{{ {pname} {ptype} }}\n")
            else:
                src.add(comment(f"{variant} is the {w.replace('_', ' ')} {thing}.") + f"type {variant} struct{{}}\n")
        for w, pname, ptype in c.cases:
            variant = marker + camel(w)
            if ptype and s.zero_invalid(ptype):
                src.add(comment(f"{n}{camel(w)} is the {w.replace('_', ' ')} {thing} with its {spoken(pname)}; "
                                f"no {spoken(pname)} is a defect.") +
                        f"func {n}{camel(w)}({pname} {ptype}) {n} {{\n" + zero_vars([ptype], "\t") +
                        f"\tif {pname} == {zero_name(ptype)} {{\n"
                        f'\t\tdefect.Raise("{s.name}.{n}{camel(w)}", "the zero {ptype.rsplit(".", 1)[-1]}")\n\t}}\n'
                        f"\treturn {variant}{{{pname}: {pname}}}\n}}\n")
            elif ptype:
                src.add(comment(f"{n}{camel(w)} is the {w.replace('_', ' ')} {thing} with its {spoken(pname)}.") +
                        f"func {n}{camel(w)}({pname} {ptype}) {n} {{ return {variant}{{{pname}: {pname}}} }}\n")
            else:
                src.add(comment(f"{n}{camel(w)} is the {w.replace('_', ' ')} {thing}.") +
                        f"func {n}{camel(w)}() {n} {{ return {variant}{{}} }}\n")
        methods = []
        for w, pname, ptype in c.cases:
            variant = marker + camel(w)
            if ptype:
                methods.append(f"func (x {variant}) Accept(v {n}Visitor) {{ v.Visit{camel(w)}(x.{pname}) }}\n")
            else:
                methods.append(f"func ({variant}) Accept(v {n}Visitor) {{ v.Visit{camel(w)}() }}\n")
        for w, _, _ in c.cases:
            methods.append(f"func ({marker + camel(w)}) {marker}() {{}}\n")
        src.add("".join(methods))

    # entity.go

    def entity_go(self):
        s = self.s
        e, idt = s.entity, s.id
        r = receiver(e)
        src = Source(s.name)
        src.need(self.kit("outcome"), self.kit("fail"), self.kit("defect"))
        self.used_wrong = True
        thing = spoken(e)
        fields = [("id", idt)] + [(p.field, p.name) for p in s.parts]
        if s.decider:
            fields.append(("closed", "opt.Option[instant.Instant]"))
            src.need(self.kit("opt"), self.kit("instant"))
        width = max(len(f) for f, _ in fields)
        body = "".join(f"\t{f.ljust(width)} {t}\n" for f, t in fields)
        made = "only Build makes one" if s.builder else f"only New{e} and Restore{e} make one"
        src.add(comment(f"{e} is one {thing} as {s.name} knows it" +
                        (", open or closed: it holds when it closed only while it is closed" if s.decider else "") +
                        f". The zero {e} is no {thing}; {made}, and every method raises a defect on it.") +
                f"type {e} struct {{\n{body}}}\n")
        # The rules every part of an entity meets, wherever it is made.
        checks = [(f, t) for f, t in fields[:len(s.parts) + 1] if s.zero_invalid(t)]
        rules = zero_vars([t for _, t in checks], "\t") + "\tvar parts []fail.Invalid\n"
        for f, t in checks:
            rules += f'\tparts = wrong(parts, {r}.{f} != {zero_name(t)}, "{s.name}.{snake(e)}.no_{snake(f)}", "{f}")\n'
        if s.decider:
            rules += (f"\t{r}.closed.Match(func(at instant.Instant) {{\n"
                      f'\t\tparts = wrong(parts, at.Known(), "{s.name}.{snake(e)}.closed_unknown", "closed")\n'
                      "\t}, func() {})\n")
        rules += "\treturn parts\n"
        if s.builder:
            self.builder(src, e, r)
        else:
            self.new_and_restore(src, e, idt, r)
        src.add(comment(f"wrongParts is every part of {r} a rule refuses, in order.") +
                f"func ({r} {e}) wrongParts() []fail.Invalid {{\n{rules}}}\n")
        src.add(comment(f"known raises a defect when {r} is the zero {e}, which no function takes; op names the function "
                        "it was handed to. Every constructor refuses a zero identity, so the identity tells.") +
                f"func ({r} {e}) known(op string) {{\n\tif {r}.id == ({idt}{{}}) {{\n"
                f'\t\tdefect.Raise(op, "the zero {e}")\n\t}}\n}}\n')
        op = f"{s.name}.{e}."
        src.add(comment(f"ID is the {thing}'s identity, which stores and other {plural(thing)} refer to it by.") +
                f'func ({r} {e}) ID() {idt} {{\n\t{r}.known("{op}ID")\n\treturn {r}.id\n}}\n')
        src.add(comment(f"IsSame says whether {r} and other are the same {thing}, whatever their parts.") +
                f'func ({r} {e}) IsSame(other {e}) bool {{\n\t{r}.known("{op}IsSame")\n\tother.known("{op}IsSame")\n'
                f"\treturn {r}.id == other.id\n}}\n")
        if s.decider:
            src.add(comment(f"CanClose says whether CommandClose applies: the {thing} is open.") +
                    f'func ({r} {e}) CanClose() bool {{\n\t{r}.known("{op}CanClose")\n\treturn {r}.closed.IsNone()\n}}\n')
            src.add(comment(f"CanReopen says whether CommandReopen applies: the {thing} is closed.") +
                    f'func ({r} {e}) CanReopen() bool {{\n\t{r}.known("{op}CanReopen")\n'
                    f"\treturn !{r}.closed.IsNone()\n}}\n")
        visits = f"\tVisit{idt}(id {idt})\n" + "".join(f"\tVisit{p.name}({p.field} {p.name})\n" for p in s.parts)
        tail = ""
        if s.decider:
            visits += "\tVisitOpen()\n\tVisitClosedAt(at instant.Instant)\n"
            tail = f"\t{r}.closed.Match(v.VisitClosedAt, v.VisitOpen)\n"
        told = f"the {thing}'s identity and parts" + (", then whether it is open or when it closed" if s.decider else "")
        src.add(comment(f"{e}Visitor is told {told}.") + f"type {e}Visitor interface {{\n{visits}}}\n")
        calls = (f'\t{r}.known("{op}Accept")\n\tv.Visit{idt}({r}.id)\n' +
                 "".join(f"\tv.Visit{p.name}({r}.{p.field})\n" for p in s.parts))
        src.add(comment(f"Accept tells v {told}.") + f"func ({r} {e}) Accept(v {e}Visitor) {{\n{calls}{tail}}}\n")
        return src.text()

    def new_and_restore(self, src, e, idt, r):
        s = self.s
        thing = spoken(e)
        params = [("id", idt)] + [(p.field, p.name) for p in s.parts]
        given = ", ".join(f"{f} {t}" for f, t in params)
        build = f"{e}{{" + ", ".join(f"{f}: {f}" for f, _ in params) + "}"
        state = ", open" if s.decider else ""
        src.add(comment(f"New{e} is a new {thing}{state}; it refuses a part that is no value, and reports every one, "
                        "in order.") +
                f"func New{e}({given}) (out outcome.Of[{e}, Fail]) {{\n\t{r} := {build}\n" + made(r))
        if s.decider:
            src.add(comment(f"Restore{e} is a stored {thing}: its parts, and when it closed (none while it is open). It "
                            f"refuses what New{e} refuses, and a closing time that is unknown.") +
                    f"func Restore{e}({given}, closed opt.Option[instant.Instant]) (out outcome.Of[{e}, Fail]) {{\n"
                    f"\t{r} := {build}\n\t{r}.closed = closed\n" + made(r))
        else:
            src.add(comment(f"Restore{e} is a stored {thing}, from its parts: only adapters call it, and it refuses "
                            f"what New{e} refuses.") +
                    f"func Restore{e}({given}) (out outcome.Of[{e}, Fail]) {{\n\t{r} := {build}\n" + made(r))

    def builder(self, src, e, r):
        s = self.s
        thing = spoken(e)
        stages = [e + p.name + "Step" for p in s.parts] + [e + "Builder"]
        for i, p in enumerate(s.parts):
            src.add(comment(f"{stages[i]} is the builder's stage for the {spoken(p.name)}. The zero {stages[i]} is a "
                            "skipped stage, and Build refuses what it leaves out.") +
                    f"type {stages[i]} struct{{ {lower_camel(stages[i])} {e} }}\n")
        src.add(comment(f"{e}Builder is the builder's last stage. The zero {e}Builder is a skipped stage, and Build "
                        "refuses what it leaves out.") + f"type {e}Builder struct{{ {lower_camel(e)}Builder {e} }}\n")
        src.add(comment(f"New{e}Builder starts {article(thing)}: its identity, then one stage per part.") +
                f"func New{e}Builder(id {s.id}) {stages[0]} {{\n"
                f"\treturn {stages[0]}{{{lower_camel(stages[0])}: {e}{{id: id}}}}\n}}\n")
        for i, p in enumerate(s.parts):
            st = stages[i]
            src.add(comment(f"{p.name} is the {thing}'s {spoken(p.name)}.") +
                    f"func (stage {st}) {p.name}({p.field} {p.name}) {stages[i + 1]} {{\n"
                    f"\t{r} := stage.{lower_camel(st)}\n\t{r}.{p.field} = {p.field}\n"
                    f"\treturn {stages[i + 1]}{{{lower_camel(stages[i + 1])}: {r}}}\n}}\n")
        state = ", open" if s.decider else ""
        src.add(comment(f"Build is the {thing}{state}, or every part a skipped stage or a value that is none left "
                        "out, in order.") +
                f"func (b {e}Builder) Build() (out outcome.Of[{e}, Fail]) {{\n\t{r} := b.{lower_camel(e)}Builder\n" +
                made(r))
        if s.decider:
            src.add(comment(f"Restore{e} is a stored {thing}: the parts b holds, and when it closed (none while it is "
                            "open). Only adapters call it; it refuses what Build refuses, and a closing time that is "
                            "unknown.") +
                    f"func Restore{e}(b {e}Builder, closed opt.Option[instant.Instant]) (out outcome.Of[{e}, Fail]) {{\n"
                    f"\t{r} := b.{lower_camel(e)}Builder\n\t{r}.closed = closed\n" + made(r))
        else:
            src.add(comment(f"Restore{e} is a stored {thing}, from the parts b holds: only adapters call it, and it "
                            "refuses what Build refuses.") +
                    f"func Restore{e}(b {e}Builder) (out outcome.Of[{e}, Fail]) {{\n"
                    f"\t{r} := b.{lower_camel(e)}Builder\n" + made(r))

    # decide.go

    def decide_go(self):
        s = self.s
        e = s.entity
        thing = spoken(e)
        v = lower_camel(e)
        src = Source(s.name)
        src.need("slices", self.kit("defect"), self.kit("fail"), self.kit("instant"), self.kit("opt"),
                 self.kit("outcome"))
        src.add(comment(f"Command is what someone asks of {article(thing)}, named for the action. The zero Command is "
                        "nil; Decide raises a defect on it.") +
                f"type Command interface {{\n\tdecide({receiver(e)} {e}, now instant.Now) outcome.Of[Decided, Fail]\n}}\n")
        src.add(comment(f"closeCommand closes an open {thing}.") + "type closeCommand struct{}\n")
        src.add(comment(f"reopenCommand opens a closed {thing} again.") + "type reopenCommand struct{}\n")
        src.add(comment(f"CommandClose closes the {thing}.") + "func CommandClose() Command { return closeCommand{} }\n")
        src.add(comment(f"CommandReopen opens the {thing} again.") +
                "func CommandReopen() Command { return reopenCommand{} }\n")
        src.add(comment("Event is what a decision changed. The zero Event is nil; only Decide makes one.") +
                "type Event interface {\n\tAccept(v EventVisitor)\n\tevent()\n}\n")
        src.add(comment("EventVisitor is told what changed.") +
                "type EventVisitor interface {\n\tVisitClosed(at instant.Instant)\n\tVisitReopened()\n}\n")
        src.add(comment(f"{v}Closed is {article(thing)} closed at a moment.") +
                f"type {v}Closed struct{{ at instant.Instant }}\n")
        src.add(comment(f"{v}Reopened is {article(thing)} opened again.") + f"type {v}Reopened struct{{}}\n")
        src.add(f"func (x {v}Closed) Accept(v EventVisitor) {{ v.VisitClosed(x.at) }}\n"
                f"func ({v}Reopened) Accept(v EventVisitor) {{ v.VisitReopened() }}\n"
                f"func ({v}Closed) event()                  {{}}\n"
                f"func ({v}Reopened) event()                {{}}\n")
        src.add(comment(f"Decided is {article(thing)} after a command, with the events that took it there. The zero "
                        "Decided is no decision; only Decide makes one, always with an event, and Then and Accept raise "
                        "a defect on it.") +
                f"type Decided struct {{\n\t{v} {e}\n\tevents []Event\n}}\n")
        src.add(comment(f"DecidedVisitor is told the {thing} as decided, then each event in order.") +
                f"type DecidedVisitor interface {{\n\tVisit{e}({v} {e})\n\tVisitEvent(event Event)\n}}\n")
        r = receiver(e)
        src.add(comment(f"Decide is what a command does to {article(thing)} at now: the next {thing} with its events, "
                        f"or why the {thing} refuses it.") +
                f"func Decide({r} {e}, cmd Command, now instant.Now) outcome.Of[Decided, Fail] {{\n"
                f'\t{r}.known("{s.name}.Decide")\n'
                f'\tif cmd == nil {{\n\t\tdefect.Raise("{s.name}.Decide", "the zero Command")\n\t}}\n'
                f"\treturn cmd.decide({r}, now)\n}}\n")
        src.add(comment(f"Then decides a further command on the {thing} as decided: both decisions' events in order, or "
                        f"why the {thing} refuses the second.") +
                "func (d Decided) Then(cmd Command, now instant.Now) (out outcome.Of[Decided, Fail]) {\n"
                f'\td.known("{s.name}.Decided.Then")\n'
                f"\tnext, f := outcome.Unpack(Decide(d.{v}, cmd, now))\n"
                "\tif f != nil {\n\t\treturn out.Err(f)\n\t}\n"
                f"\treturn out.Ok(Decided{{{v}: next.{v}, events: slices.Concat(d.events, next.events)}})\n}}\n")
        src.add(comment(f"Accept tells v the {thing} as decided, then each event in order.") +
                f'func (d Decided) Accept(v DecidedVisitor) {{\n\td.known("{s.name}.Decided.Accept")\n\tv.Visit{e}(d.{v})\n'
                "\tfor _, e := range d.events {\n\t\tv.VisitEvent(e)\n\t}\n}\n")
        src.add(comment("known raises a defect when d is the zero Decided, which holds no event; op names the function "
                        "it was handed to.") +
                'func (d Decided) known(op string) {\n\tif len(d.events) == 0 {\n'
                '\t\tdefect.Raise(op, "the zero Decided")\n\t}\n}\n')
        src.add(f"func (closeCommand) decide({r} {e}, now instant.Now) (out outcome.Of[Decided, Fail]) {{\n"
                f"\tif !{r}.CanClose() {{\n"
                f'\t\treturn out.Err(refused{{fail.NewRefusal(fail.NewCode("{s.name}.{snake(e)}.already_closed"))}})\n'
                f"\t}}\n\treturn out.Ok(decided({r}, {v}Closed{{at: now.Stamp()}}))\n}}\n")
        src.add(f"func (reopenCommand) decide({r} {e}, _ instant.Now) (out outcome.Of[Decided, Fail]) {{\n"
                f"\tif !{r}.CanReopen() {{\n"
                f'\t\treturn out.Err(refused{{fail.NewRefusal(fail.NewCode("{s.name}.{snake(e)}.already_open"))}})\n'
                f"\t}}\n\treturn out.Ok(decided({r}, {v}Reopened{{}}))\n}}\n")
        src.add(comment(f"decided is {r} after e, with e as its event.") +
                f"func decided({r} {e}, e Event) Decided {{ return Decided{{{v}: evolve({r}, e), events: []Event{{e}}}} }}\n")
        src.add(comment(f"evolve is {r} after e. It stays unexported: only Decide applies an event, to the state it was "
                        "decided on.") +
                f"func evolve({r} {e}, e Event) {e} {{\n\tswitch e := e.(type) {{\n"
                f"\tcase {v}Closed:\n\t\t{r}.closed = opt.Some(e.at)\n"
                f"\tcase {v}Reopened:\n\t\t{r}.closed = opt.None[instant.Instant]()\n\t}}\n\treturn {r}\n}}\n")
        return src.text()


def made(r):
    """The end of a constructor that checks every part of r."""
    return (f"\tif parts := {r}.wrongParts(); len(parts) > 0 {{\n\t\treturn out.Err(invalidParts(parts))\n\t}}\n"
            f"\treturn out.Ok({r})\n}}\n")


def zero_name(t):
    return "no" + t.rsplit(".", 1)[-1]


def zero_vars(types, indent, p=None):
    """One var per distinct type holding its zero value: a literal of another package's type is refused outside it.
    In the package's black-box tests (p, its name) the package's own types are p.T."""
    seen, out = set(), ""
    for t in types:
        if t not in seen:
            seen.add(t)
            out += f"{indent}var {zero_name(t)} {qualified(p, t) if p else t}\n"
    return out


def or_list(items):
    return items[0] if len(items) == 1 else ", ".join(items[:-1]) + " or " + items[-1]


def and_list(items):
    return items[0] if len(items) == 1 else ", ".join(items[:-1]) + " and " + items[-1]


def rules_test(name, module):
    return (f"package {name}_test\n\nimport (\n\t\"testing\"\n\n\t\"{module}/internal/archguard/domainrules\"\n)\n\n"
            "// The domain rules for this package and the code that imports it (docs/server/architecture.md \"Domain "
            "model\").\nfunc TestDomainRules(t *testing.T) { domainrules.Check(t) }\n")


# The core's black-box tests.

class Tests:
    def __init__(self, s):
        self.s = s
        self.src = Source(s.name + "_test")
        self.p = s.name
        self.src.need("testing", s.module + "/" + s.dir, s.module + "/" + KIT + "domaintest")
        self.helpers = {}

    def kit(self, pkg):
        self.src.need(self.s.module + "/" + KIT + pkg)

    def sample(self, t):
        """An expression for a valid value of type t, inside a test with t *testing.T."""
        s = self.s
        if t in FOREIGN:
            self.src.need(s.module + "/" + FOREIGN[t][0])
            if t == "instant.Instant":
                self.at()
            if t == "slot.Slot":
                self.helpers["first"] = "// first is the caller's first place: the zero Slot.\nvar first slot.Slot\n"
            if t.startswith("agentref."):
                self.kit("domaintest")
            return FOREIGN_SAMPLE[t]
        x = s.local(t)
        if isinstance(x, Closed):
            w, _, pt = x.cases[0]
            return f"{self.p}.{t}{camel(w)}({self.sample(pt) if pt else ''})"
        return f"some{t}(t)" if self.helper(x) else ""

    def helper(self, x):
        """A helper that makes a valid x for the tests."""
        s, n = self.s, x.name
        key = f"some{n}"
        if key in self.helpers:
            return True
        self.helpers[key] = None  # reserve, in case of recursion
        if isinstance(x, Value) and x.simple:
            raw = f'"{spoken(x.field)} 1"' if x.kind == "string" else "1"
            make = (f"{self.p}.Parse{n}({raw})" if self.plain_parse(x) else f"domaintest.Ok(t, {self.p}.Parse{n}({raw}))")
        elif isinstance(x, Value):
            args = ", ".join(self.sample(t) for _, t in x.parts)
            checked = any(s.zero_invalid(t) for _, t in x.parts)
            make = f"domaintest.Ok(t, {self.p}.New{n}({args}))" if checked else f"{self.p}.New{n}({args})"
        else:
            args = ", ".join(sample_raw(raw, kind) for raw, kind, _ in x.raws)
            make = f"domaintest.Ok(t, {self.p}.Parse{n}({args}))"
        self.helpers[key] = (comment(f"{key} is a valid {spoken(n)}.") +
                             f"func {key}(t *testing.T) {self.p}.{n} {{\n\tt.Helper()\n\treturn {make}\n}}\n")
        return True

    def plain_parse(self, v):
        return v.kind == "string" and v.zero_is is not None

    def raises(self, op, what, call):
        """A line that fails the test unless call raises the defect op names for the zero what."""
        self.src.need(self.s.module + "/" + KIT + "defect")
        self.helpers["raises"] = (
            comment('raises fails the test unless do raises the defect want ("<op>: <cause>").') +
            "func raises(t *testing.T, want string, do func()) {\n\tt.Helper()\n\tdefer func() {\n\t\tt.Helper()\n"
            "\t\tr := recover()\n\t\tif d, ok := r.(defect.Defect); !ok || d.Error() != want {\n"
            '\t\t\tt.Errorf("raised %v, want the defect %q", r, want)\n\t\t}\n\t}()\n\tdo()\n}\n')
        return f'\traises(t, "{op}: the zero {what}", func() {{ {call} }})\n'

    def at(self):
        self.src.need("time", self.s.module + "/" + KIT + "instant")
        self.helpers["at"] = ("// at is the moment the tests' instants and clock read.\n"
                              "var at = instant.At(time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC))\n")

    def text(self):
        s = self.s
        body = []
        for v in s.values:
            body += self.value_tests(v)
        for p in s.parsed:
            body += self.parsed_tests(p)
        for c in s.closed:
            body += self.closed_tests(c)
        if s.entity:
            body += self.entity_tests()
        if s.decider:
            body += self.decider_tests()
        for h in self.helpers.values():
            if h:
                self.src.add(h)
        for b in body:
            self.src.add(b)
        return self.src.text()

    def check(self, call, want):
        """Lines that fail the test unless call fails as want (a Go string literal) says."""
        return (f"\tgot := domaintest.Fails(t, {call})\n\tif want := {go_string(want)}; got != want {{\n"
                f'\t\tt.Errorf("got %q, want %q", got, want)\n\t}}\n')

    def value_tests(self, v):
        s, n, p = self.s, v.name, self.p
        if v.simple:
            zero = f"\tvar zero {p}.{n}\n"
            if v.zero_is is not None and v.kind == "string":
                return [f"func TestParse{n}OfEmptyTextIsTheZero{n}(t *testing.T) {{\n{zero}"
                        f'\tif {p}.Parse{n}("") != zero {{\n\t\tt.Error("Parse{n} of empty text is not {v.zero_is}")\n'
                        f'\t}}\n\tif {p}.Parse{n}("{spoken(v.field)} 1") == zero {{\n'
                        f'\t\tt.Error("Parse{n} of a text made {v.zero_is}")\n\t}}\n}}\n']
            if v.zero_is is not None:
                return [f"func TestParse{n}OfZeroIsTheZero{n}(t *testing.T) {{\n{zero}"
                        f"\tif domaintest.Ok(t, {p}.Parse{n}(0)) != zero {{\n"
                        f'\t\tt.Error("Parse{n}(0) is not {v.zero_is}")\n\t}}\n}}\n',
                        f"func TestParse{n}RefusesLessThanZero(t *testing.T) {{\n" +
                        self.check(f"{p}.Parse{n}(-1)", f'"invalid {s.name}.{snake(v.field)}.negative {v.field}"') + "}\n"]
            bad, word = bad_input(v)
            self.helper(v)
            return [f"func TestParse{n}NeverMakesTheZero{n}(t *testing.T) {{\n{zero}"
                    f"\tif some{n}(t) == zero {{\n\t\tt.Error(\"Parse{n} made the zero {n}\")\n\t}}\n}}\n",
                    f"func TestParse{n}RefusesInvalidInput(t *testing.T) {{\n" +
                    self.check(f"{p}.Parse{n}({bad})", f'"invalid {s.name}.{snake(v.field)}.{word} {v.field}"') + "}\n"]
        checked = [(part, t) for part, t in v.parts if s.zero_invalid(t)]
        if not checked:
            for _, t in v.parts:
                self.src.need(*s.imports_for(t))
            zeros = zero_vars([t for _, t in v.parts], "\t", p)
            args = ", ".join(zero_name(t) for _, t in v.parts)
            return [f"func TestNew{n}OfZeroPartsIsTheZero{n}(t *testing.T) {{\n\tvar zero {p}.{n}\n{zeros}"
                    f"\tif {p}.New{n}({args}) != zero {{\n\t\tt.Error(\"New{n} of zero parts is not the zero {n}\")\n"
                    "\t}\n}\n"]
        self.helper(v)
        out = [f"func TestNew{n}NeverMakesTheZero{n}(t *testing.T) {{\n\tvar zero {p}.{n}\n"
               f"\tif some{n}(t) == zero {{\n\t\tt.Error(\"New{n} made the zero {n}\")\n\t}}\n}}\n",
               f"func TestTheZero{n}IsADefect(t *testing.T) {{\n\tvar zero {p}.{n}\n" +
               self.raises(f"{s.name}.{n}.Accept", n, "zero.Accept(nil)") + "}\n"]
        zeros = zero_vars([t for _, t in checked], "\t", p)
        args = ", ".join(zero_name(t) if (part, t) in checked else self.sample(t) for part, t in v.parts)
        want = "; ".join(f"invalid {s.name}.{snake(n)}.no_{snake(part)} {part}" for part, _ in checked)
        out.append(f"func TestNew{n}RefusesPartsThatAreNoValue(t *testing.T) {{\n{zeros}" +
                   self.check(f"{p}.New{n}({args})", f'"{want}"') + "}\n")
        return out

    def parsed_tests(self, x):
        s, n, p = self.s, x.name, self.p
        self.helper(x)
        if s.zero_invalid(n):
            out = [f"func TestParse{n}NeverMakesTheZero{n}(t *testing.T) {{\n\tvar zero {p}.{n}\n"
                   f"\tif some{n}(t) == zero {{\n\t\tt.Error(\"Parse{n} made the zero {n}\")\n\t}}\n}}\n",
                   f"func TestTheZero{n}IsADefect(t *testing.T) {{\n\tvar zero {p}.{n}\n" +
                   self.raises(f"{s.name}.{n}.Accept", n, "zero.Accept(nil)") + "}\n"]
        else:
            empty = ", ".join('""' if kind == "string" else "0" for _, kind, _ in x.raws)
            out = [f"func TestParse{n}OfEmptyPartsIsTheZero{n}(t *testing.T) {{\n\tvar zero {p}.{n}\n"
                   f"\tif domaintest.Ok(t, {p}.Parse{n}({empty})) != zero {{\n"
                   f'\t\tt.Error("Parse{n} of empty parts is not the zero {n}")\n\t}}\n}}\n']
        ruled = [v for _, _, v in x.raws if bad_input(v)]
        if ruled:
            args = ", ".join(bad_input(v)[0] if bad_input(v) else sample_raw(raw, kind) for raw, kind, v in x.raws)
            want = "; ".join(f"invalid {s.name}.{snake(v.field)}.{bad_input(v)[1]} {v.field}" for v in ruled)
            out.append(f"func TestParse{n}ReportsEveryInvalidPart(t *testing.T) {{\n" +
                       self.check(f"{p}.Parse{n}({args})", f'"{want}"') + "}\n")
        # One behaviour: equal parts make equal values, which key maps.
        out.append(f"func TestEqualPartsMakeEqual{plural(n)}(t *testing.T) {{\n"
                   f"\tseen := map[{p}.{n}]bool{{some{n}(t): true}}\n"
                   f"\tif !seen[some{n}(t)] {{\n\t\tt.Error(\"two {plural(spoken(n))} of the same parts are not the same "
                   "key\")\n\t}\n}\n")
        # One test visitor: Accept tells the parts as Parse took them.
        fields = "".join(f"\t{raw} {p}.{v.name}\n" for raw, _, v in x.raws)
        params = ", ".join(f"{raw} {p}.{v.name}" for raw, _, v in x.raws)
        sets = ", ".join(f"r.{raw}" for raw, _, _ in x.raws) + " = " + ", ".join(raw for raw, _, _ in x.raws)
        name = lower_camel(n) + "Parts"
        checks = ""
        for raw, kind, v in x.raws:
            self.helper(v)
            checks += (f"\tif got.{raw} != some{v.name}(t) {{\n"
                       f"\t\tt.Error(\"Accept told another {spoken(raw)}\")\n\t}}\n")
        out.append(comment(f"{name} is the test's own visitor: it keeps the parts it is told.") +
                   f"type {name} struct {{\n{fields}}}\n\n"
                   f"func (r *{name}) Visit{n}({params}) {{ {sets} }}\n\n"
                   f"func Test{n}AcceptTellsItsParts(t *testing.T) {{\n\tvar got {name}\n"
                   f"\tsome{n}(t).Accept(&got)\n{checks}}}\n")
        return out

    def closed_tests(self, c):
        p, n = self.p, c.name
        name = lower_camel(n) + "Names"
        methods = ""
        for w, pname, ptype in c.cases:
            if ptype:
                self.src.need(*self.s.imports_for(ptype))
                methods += (f'func (r *{name}) Visit{camel(w)}({qualified(p, ptype)}) '
                            f'{{ r.names = append(r.names, "{w}") }}\n')
            else:
                methods += f'func (r *{name}) Visit{camel(w)}() {{ r.names = append(r.names, "{w}") }}\n'
        values = ", ".join(f"{p}.{n}{camel(w)}({self.sample(pt) if pt else ''})" for w, _, pt in c.cases)
        want = ", ".join(f'"{w}"' for w, _, _ in c.cases)
        self.src.need("slices")
        if c.plain:
            zero = (f"func TestTheZero{n}IsADefect(t *testing.T) {{\n\tvar zero {p}.{n}\n" +
                    self.raises(f"{self.s.name}.{n}.Accept", n, "zero.Accept(nil)") + "}\n")
        else:
            checked = [(w, pt) for w, _, pt in c.cases if pt and self.s.zero_invalid(pt)]
            zero = ""
            if checked:
                for _, pt in checked:
                    self.src.need(*self.s.imports_for(pt))
                zero = (f"func Test{n}RefusesPayloadsThatAreNoValue(t *testing.T) {{\n" +
                        zero_vars([pt for _, pt in checked], "\t", p) +
                        "".join(self.raises(f"{self.s.name}.{n}{camel(w)}", pt.rsplit(".", 1)[-1],
                                            f"{p}.{n}{camel(w)}({zero_name(pt)})") for w, pt in checked) + "}\n")
        tests = [zero] if zero else []
        return tests + [comment(f"{name} is the test's own visitor: it keeps the name of each {spoken(n)} it is told.") +
                f"type {name} struct{{ names []string }}\n\n{methods}\n"
                f"func Test{n}AcceptTellsEach{n}(t *testing.T) {{\n\tvar got {name}\n"
                f"\tfor _, x := range []{p}.{n}{{{values}}} {{\n\t\tx.Accept(&got)\n\t}}\n"
                f"\tif want := []string{{{want}}}; !slices.Equal(got.names, want) {{\n"
                f'\t\tt.Errorf("Accept told %q, want %q", got.names, want)\n\t}}\n}}\n']

    def chain(self, missing=None):
        """The call that makes the entity, every part valid but the one named missing (its zero var)."""
        s, p, e = self.s, self.p, self.s.entity
        def arg(f, t):
            return zero_name(t) if f == missing else self.sample(t)
        if s.builder:
            call = f"{p}.New{e}Builder({arg('id', s.id)})"
            for part in s.parts:
                call += f".\n\t\t{part.name}({arg(part.field, part.name)})"
            return call
        args = ", ".join([arg("id", s.id)] + [arg(part.field, part.name) for part in s.parts])
        return f"{p}.New{e}({args})"

    def built(self):
        return f"{self.chain()}.\n\t\tBuild()" if self.s.builder else self.chain()

    def entity_tests(self):
        s, p, e = self.s, self.p, self.s.entity
        idt = s.id
        out = []
        maker = "Build" if s.builder else f"New{e}"
        for f, t in [("id", idt)] + [(part.field, part.name) for part in s.parts]:
            if not s.zero_invalid(t):
                continue
            call = self.chain(missing=f) + (".\n\t\tBuild()" if s.builder else "")
            out.append(f"func Test{maker}Refuses{e}Without{'ID' if f == 'id' else f[:1].upper() + f[1:]}"
                       f"(t *testing.T) {{\n\tvar {zero_name(t)} {qualified(p, t)}\n" +
                       self.check(call, f'"invalid {s.name}.{snake(e)}.no_{snake(f)} {f}"') + "}\n")
        if s.builder and s.parts:
            last = e + s.parts[-1].name + "Step"
            skipped = [("id", idt)] + [(part.field, part.name) for part in s.parts[:-1]]
            want = "; ".join(f"invalid {s.name}.{snake(e)}.no_{snake(f)} {f}" for f, t in skipped if s.zero_invalid(t))
            if want:
                out.append(f"func TestBuildListsEveryPartASkippedStageLeftOut(t *testing.T) {{\n"
                           f"\tvar skipped {p}.{last} // a skipped stage still compiles; Build refuses it\n" +
                           self.check(f"skipped.{s.parts[-1].name}({self.sample(s.parts[-1].name)}).Build()",
                                      f'"{want}"') + "}\n")
        # Every method raises a defect on the zero entity, which no constructor makes.
        op = f"{s.name}.{e}."
        zero = (f"func TestTheZero{e}IsADefect(t *testing.T) {{\n\tvar zero {p}.{e}\n"
                f"\tsome := domaintest.Ok(t, {self.built()})\n" +
                self.raises(op + "ID", e, "zero.ID()") +
                self.raises(op + "IsSame", e, "zero.IsSame(some)") +
                self.raises(op + "IsSame", e, "some.IsSame(zero)") +
                self.raises(op + "Accept", e, "zero.Accept(nil)"))
        if s.decider:
            self.src.need(s.module + "/" + KIT + "clock/clocktest", "time")
            zero += (self.raises(op + "CanClose", e, "zero.CanClose()") +
                     self.raises(op + "CanReopen", e, "zero.CanReopen()") +
                     "\tnow := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()\n" +
                     self.raises(f"{s.name}.Decide", e, f"{p}.Decide(zero, {p}.CommandClose(), now)"))
        out.append(zero + "}\n")
        if not s.decider:
            # One behaviour: the same identity is the same entity, whatever its parts.
            out.append(f"func TestTwo{plural(e)}OfOneIdentityAreTheSame(t *testing.T) {{\n"
                       f"\ta := domaintest.Ok(t, {self.built()})\n"
                       f"\tif !a.IsSame(domaintest.Ok(t, {self.built()})) {{\n"
                       f'\t\tt.Error("two {plural(spoken(e))} of one identity are not the same")\n\t}}\n}}\n')
        # A test visitor over the entity.
        name = lower_camel(e) + "Parts"
        methods = f"func (r *{name}) Visit{idt}(id {p}.{idt}) {{ r.id = id }}\n"
        for part in s.parts:
            methods += f"func (r *{name}) Visit{part.name}({p}.{part.name}) {{}}\n"
        fields = f"\tid {p}.{idt}\n"
        if s.decider:
            self.kit("instant")
            fields += "\tclosed bool\n"
            methods += (f"func (r *{name}) VisitOpen() {{ r.closed = false }}\n"
                        f"func (r *{name}) VisitClosedAt(instant.Instant) {{ r.closed = true }}\n")
        self.helper(s.local(idt))
        out.append(comment(f"{name} is the test's own visitor: it keeps the identity it is told" +
                           (" and whether the " + spoken(e) + " is closed." if s.decider else ".")) +
                   f"type {name} struct {{\n{fields}}}\n\n{methods}\n"
                   f"func Test{e}AcceptTellsItsIdentity(t *testing.T) {{\n\tvar got {name}\n"
                   f"\tdomaintest.Ok(t, {self.built()}).Accept(&got)\n"
                   f"\tif got.id != some{idt}(t) {{\n\t\tt.Error(\"Accept told another identity\")\n\t}}\n}}\n")
        return out

    def decider_tests(self):
        s, p, e = self.s, self.p, self.s.entity
        self.kit("opt")
        self.kit("instant")
        self.src.need(s.module + "/" + KIT + "clock/clocktest", "time", "slices")
        out = []
        if s.builder:
            restore = f"{p}.Restore{e}({self.chain()}, opt.Some(unknown))"
            restored = f"{p}.Restore{e}({self.chain()}, opt.Some(at))"
        else:
            args = ", ".join([self.sample(s.id)] + [self.sample(part.name) for part in s.parts])
            restore = f"{p}.Restore{e}({args}, opt.Some(unknown))"
            restored = f"{p}.Restore{e}({args}, opt.Some(at))"
        self.at()
        out.append(f"func TestRestore{e}RefusesAnUnknownClosingTime(t *testing.T) {{\n\tvar unknown instant.Instant\n" +
                   self.check(restore, f'"invalid {s.name}.{snake(e)}.closed_unknown closed"') + "}\n")
        out.append(f"func TestARestoredClosed{e}CanReopen(t *testing.T) {{\n"
                   f"\tif !domaintest.Ok(t, {restored}).CanReopen() {{\n"
                   f'\t\tt.Error("a {spoken(e)} restored closed cannot reopen")\n\t}}\n}}\n')
        name = "events"
        out.append(comment(f"{name} is the test's own visitor: it keeps the {spoken(e)} as decided and the name of each "
                           "event.") +
                   f"type {name} struct {{\n\tdecided {p}.{e}\n\tnames []string\n}}\n\n"
                   f"func (r *{name}) Visit{e}(x {p}.{e})       {{ r.decided = x }}\n"
                   f"func (r *{name}) VisitEvent(e {p}.Event)        {{ e.Accept(r) }}\n"
                   f'func (r *{name}) VisitClosed(instant.Instant) {{ r.names = append(r.names, "closed") }}\n'
                   f'func (r *{name}) VisitReopened()              {{ r.names = append(r.names, "reopened") }}\n')
        out.append(f"func TestTheZeroDecidedIsADefect(t *testing.T) {{\n\tvar zero {p}.Decided\n"
                   "\tnow := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()\n" +
                   self.raises(f"{s.name}.Decided.Accept", "Decided", "zero.Accept(nil)") +
                   self.raises(f"{s.name}.Decided.Then", "Decided", f"zero.Then({p}.CommandClose(), now)") + "}\n")
        out.append(f"func TestCloseThenReopen(t *testing.T) {{\n"
                   "\tnow := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()\n"
                   f"\topen := domaintest.Ok(t, {self.built()})\n"
                   f"\tdecided := domaintest.Ok(t, {p}.Decide(open, {p}.CommandClose(), now))\n"
                   f"\ttwice := domaintest.Fails(t, decided.Then({p}.CommandClose(), now))\n"
                   f'\tif want := "refused {s.name}.{snake(e)}.already_closed"; twice != want {{\n'
                   f'\t\tt.Errorf("closing twice: got %q, want %q", twice, want)\n\t}}\n'
                   f"\tvar seen {name}\n"
                   f"\tdomaintest.Ok(t, decided.Then({p}.CommandReopen(), now)).Accept(&seen)\n"
                   '\tif want := []string{"closed", "reopened"}; !slices.Equal(seen.names, want) {\n'
                   '\t\tt.Errorf("events %q, want %q", seen.names, want)\n\t}\n'
                   f"\tif !seen.decided.CanClose() {{\n"
                   f'\t\tt.Error("a reopened {spoken(e)} cannot close")\n\t}}\n}}\n')
        return out


def go_string(literal):
    """A Go string literal of failures joined by "; ", split into a sum of literals at each "; " when the line would
    be long."""
    text = literal[1:-1]
    if len(text) < 60 or "; " not in text:
        return literal
    pieces = text.split("; ")
    return " +\n\t\t".join(f'"{p}; "' for p in pieces[:-1]) + f' +\n\t\t"{pieces[-1]}"'


def bad_input(v):
    """A raw input a simple value refuses and the word its code ends with, or None for a text whose zero is a value."""
    if v.kind == "string":
        return None if v.zero_is is not None else ('" "', "empty")
    return ("-1", "negative") if v.zero_is is not None else ("0", "not_positive")


def qualified(p, t):
    return t if "." in t else f"{p}.{t}"


def sample_raw(raw, kind):
    return f'"{spoken(raw)} 1"' if kind == "string" else "1"


# The adapter.

class Adapter:
    def __init__(self, s):
        self.s = s
        self.p = s.name
        self.a = s.name + "adapt"
        self.core = s.module + "/" + s.dir
        self.kit = lambda pkg: s.module + "/" + KIT + pkg

    def files(self):
        s = self.s
        return {
            "doc.go": comment(f"Package {self.a} converts between {s.name} and its stored records.") + "//\n"
                      f"//persona:layer adapter\npackage {self.a}\n",
            f"{self.a}.go": self.code(),
            "rules_test.go": rules_test(self.a, s.module),
            f"{self.a}_test.go": self.tests(),
        }

    def record_fields(self):
        """(Go field, Go type, json key, omitempty, source) per stored field, source a Value, a Closed or "closed"."""
        s = self.s
        x = s.primary
        if isinstance(x, str):  # the entity
            out = [("ID", "string", "id", False, s.local(s.id))]
            for part in s.parts:
                if isinstance(part, Value):
                    out.append((part.name, part.kind, snake(part.name), part.zero_is is not None, part))
                else:
                    out.append((part.name, "string", snake(part.name), False, part))
            if s.decider:
                out.append(("ClosedAt", "*time.Time", "closed_at", True, "closed"))
            return out
        if isinstance(x, Parsed):
            return [(camel(raw), kind, snake(raw), False, v) for raw, kind, v in x.raws]
        return [(x.name, x.kind, snake(x.name), x.zero_is is not None, x)]

    def code(self):
        s, p = self.s, self.p
        src = Source(self.a)
        src.need(self.core, self.kit("outcome"), self.kit("fail"))
        fields = self.record_fields()
        width = max(len(f) for f, *_ in fields)
        twidth = max(len(t) for _, t, *_ in fields)
        body = "".join(f"\t{f.ljust(width)} {t.ljust(twidth)} `json:\"{k}{',omitempty' if o else ''}\"`\n"
                       for f, t, k, o, _ in fields)
        x = s.primary
        what = spoken(x) if isinstance(x, str) else spoken(x.name)
        typ = f"{p}.{x}" if isinstance(x, str) else f"{p}.{x.name}"
        timed = any(src_ == "closed" for *_, src_ in fields)
        if timed:
            src.need("time")
        src.add(comment(f"Record is the stored {what}: plain and tagged, owned by this adapter.") +
                f"type Record struct {{\n{body}}}\n")
        src.add(self.restore(src, x, typ, what, fields))
        src.add(self.store(src, x, typ, what, fields, timed))
        src.add(self.log(src, x, typ, what))
        if s.act:
            e = s.entity
            src.add(comment(f"Act stores what a decision decided: the {what}'s record after it, from prev, written "
                            "through save.") +
                    f"func Act(prev Record, d {p}.Decided, save func(Record) error) error {{\n"
                    "\ta := actor{prev: prev}\n\td.Accept(&a)\n\treturn save(a.record)\n}\n")
            src.add(comment("actor reads a decision: the record of what it decided, from prev.") +
                    "type actor struct {\n\tprev   Record\n\trecord Record\n}\n")
            src.add(f"func (a *actor) Visit{e}(x {p}.{e}) {{ a.record = Store(a.prev, x) }}\n")
            src.add(comment("VisitEvent is told each event the decision made. The record already holds what they "
                            "changed; an effect beyond the store, such as a notice, is read here.") +
                    f"func (a *actor) VisitEvent({p}.Event) {{}}\n")
        for c in s.closed:
            if c in [f[4] for f in fields]:
                src.add(self.closed_words(c))
        return src.text()

    def restore(self, src, x, typ, what, fields):
        s, p = self.s, self.p
        if isinstance(x, str):
            body, names = "", {}
            for f, _, _, _, source in fields:
                if isinstance(source, Value):
                    var = "id" if f == "ID" else source.field
                    if var in ("r", "out", "f", "fail", "outcome", "opt", "instant", "time", p):
                        var += "Part"
                    names[source.name] = var
                    if source.kind == "string" and source.zero_is is not None:
                        body += f"\t{var} := {p}.Parse{source.name}(r.{f})\n"
                    else:
                        body += (f"\t{var}, f := outcome.Unpack({p}.Parse{source.name}(r.{f}))\n"
                                 "\tif f != nil {\n\t\treturn out.Err(f)\n\t}\n")
                elif isinstance(source, Closed):
                    names[source.name] = f"stored{source.name}(r.{f})"
            if s.builder:
                chain = f"{p}.New{s.entity}Builder({names[s.id]})"
                for part in s.parts:
                    chain += f".\n\t\t{part.name}({names[part.name]})"
                call = f"{p}.Restore{s.entity}({chain}" + (", closedAt(r.ClosedAt))" if s.decider else ")")
            else:
                args = ", ".join([names[s.id]] + [names[part.name] for part in s.parts])
                call = f"{p}.Restore{s.entity}({args}" + (", closedAt(r.ClosedAt))" if s.decider else ")")
            text = (comment(f"Restore is the stored {what} r holds: a part that does not parse fails with {s.name}'s "
                            "failure, and a stored word this adapter does not know goes in as the zero value, which "
                            f"{s.name} refuses.") +
                    f"func Restore(r Record) (out outcome.Of[{typ}, fail.Fail]) {{\n{body}"
                    f"\treturn outcome.Widen({call})\n}}\n")
            if s.decider:
                src.need(self.kit("opt"), self.kit("instant"))
                text += (comment(f"closedAt is when a stored {what} closed: none while it is open. A stored zero time "
                                 "is an unknown instant, which Restore refuses.") +
                         "func closedAt(t *time.Time) opt.Option[instant.Instant] {\n"
                         "\tif t == nil {\n\t\treturn opt.None[instant.Instant]()\n\t}\n"
                         "\treturn opt.Some(instant.At(*t))\n}\n")
            return text
        if isinstance(x, Parsed):
            args = ", ".join(f"r.{f}" for f, *_ in fields)
            return (comment(f"Restore is the stored {what} r holds; a part that does not parse fails with {s.name}'s "
                            "failure.") +
                    f"func Restore(r Record) outcome.Of[{typ}, fail.Fail] {{\n"
                    f"\treturn outcome.Widen({p}.Parse{x.name}({args}))\n}}\n")
        if x.kind == "string" and x.zero_is is not None:
            return (comment(f"Restore is the stored {what} r holds; every text is one.") +
                    f"func Restore(r Record) (out outcome.Of[{typ}, fail.Fail]) {{\n"
                    f"\treturn out.Ok({p}.Parse{x.name}(r.{x.name}))\n}}\n")
        return (comment(f"Restore is the stored {what} r holds; one that does not parse fails with {s.name}'s "
                        "failure.") +
                f"func Restore(r Record) outcome.Of[{typ}, fail.Fail] {{\n"
                f"\treturn outcome.Widen({p}.Parse{x.name}(r.{x.name}))\n}}\n")

    def store(self, src, x, typ, what, fields, timed):
        s, p = self.s, self.p
        if isinstance(x, str):
            e = s.entity
            params = "prev Record, x " + typ if timed else "x " + typ
            start = "\tr := recorder{prev: prev}\n" if timed else "\tvar r recorder\n"
            doc = (f"Store is x as stored. prev is the record x was restored from (the zero Record for a new {what}): "
                   "a closing time x did not change keeps prev's bytes, so the stored zone stays as it was."
                   if timed else f"Store is x as stored.")
            text = comment(doc) + f"func Store({params}) Record {{\n{start}\tx.Accept(&r)\n\treturn r.record\n}}\n"
            words = [c for c in s.parts if isinstance(c, Closed)]
            rf = "\trecord Record\n" + ("\tprev   Record\n" if timed else "")
            for c in words:
                rf += f"\t{lower_camel(c.name)} {lower_camel(c.name)}Word\n"
            text += (comment(f"recorder fills a Record from {article(what)}. The visitors it reads closed sets with are "
                             "fields, so the nested Accept calls do not allocate.") +
                     f"type recorder struct {{\n{rf}}}\n")
            methods = f"func (r *recorder) Visit{s.id}(id {p}.{s.id}) {{ r.record.ID = id.Wire() }}\n\n"
            for part in s.parts:
                if isinstance(part, Value):
                    methods += (f"func (r *recorder) Visit{part.name}(x {p}.{part.name}) "
                                f"{{ r.record.{part.name} = x.Wire() }}\n\n")
                else:
                    w = lower_camel(part.name)
                    methods += (f"func (r *recorder) Visit{part.name}(x {p}.{part.name}) {{\n"
                                f"\tx.Accept(&r.{w})\n\tr.record.{part.name} = r.{w}.word\n}}\n\n")
            if s.decider:
                src.need(self.kit("instant"))
                methods += ("func (r *recorder) VisitOpen() { r.record.ClosedAt = nil }\n\n"
                            "func (r *recorder) VisitClosedAt(at instant.Instant) {\n"
                            "\tif r.prev.ClosedAt != nil && instant.At(*r.prev.ClosedAt).Equal(at) {\n"
                            "\t\tkept := *r.prev.ClosedAt\n\t\tr.record.ClosedAt = &kept\n\t\treturn\n\t}\n"
                            "\tclosed := time.Unix(0, at.Wire()).UTC()\n\tr.record.ClosedAt = &closed\n}\n")
            return text + methods
        if isinstance(x, Parsed):
            params = ", ".join(f"{raw} {p}.{v.name}" for raw, _, v in x.raws)
            sets = "".join(f"\tr.record.{camel(raw)} = {raw}.Wire()\n" for raw, _, _ in x.raws)
            return (comment(f"Store is x as stored.") +
                    f"func Store(x {typ}) Record {{\n\tvar r recorder\n\tx.Accept(&r)\n\treturn r.record\n}}\n" +
                    comment(f"recorder fills a Record from {article(what)}.") +
                    "type recorder struct{ record Record }\n\n"
                    f"func (r *recorder) Visit{x.name}({params}) {{\n{sets}}}\n")
        return comment("Store is x as stored.") + f"func Store(x {typ}) Record {{ return Record{{{x.name}: x.Wire()}} }}\n"

    def log(self, src, x, typ, what):
        s, p = self.s, self.p
        if isinstance(x, str):
            idv = s.local(s.id)
            return (comment(f"Log is x for a log line, and only for one: its identity.") +
                    f"func Log(x {typ}) string {{ return x.ID().Wire() }}\n" if idv.kind == "string" else "")
        if isinstance(x, Parsed):
            parts = []
            for raw, kind, _ in x.raws:
                if kind == "int":
                    src.need("strconv")
                    parts.append(f"strconv.Itoa(r.record.{camel(raw)})")
                else:
                    parts.append(f"r.record.{camel(raw)}")
            joined = ' + "/" + '.join(parts)
            return (comment(f"Log is x for a log line, and only for one: its parts, joined by /.") +
                    f"func Log(x {typ}) string {{\n\tvar r recorder\n\tx.Accept(&r)\n\treturn {joined}\n}}\n")
        if x.kind == "int":
            src.need("strconv")
            return (comment("Log is x for a log line, and only for one.") +
                    f"func Log(x {typ}) string {{ return strconv.Itoa(x.Wire()) }}\n")
        return comment("Log is x for a log line, and only for one.") + f"func Log(x {typ}) string {{ return x.Wire() }}\n"

    def closed_words(self, c):
        p, n = self.p, c.name
        w = lower_camel(n)
        cases = "".join(f'\tcase "{word}":\n\t\treturn {p}.{n}{camel(word)}()\n' for word, _, _ in c.cases)
        visits = "".join(f'func (x *{w}Word) Visit{camel(word)}() {{ x.word = "{word}" }}\n' for word, _, _ in c.cases)
        return (comment(f"stored{n} is a stored {spoken(n)} as {self.s.name} knows it; a word this adapter does not know "
                        f"is the zero {n}, which {self.s.name} refuses.") +
                f"func stored{n}(word string) {p}.{n} {{\n\tswitch word {{\n{cases}\t}}\n"
                f"\tvar unknown {p}.{n}\n\treturn unknown\n}}\n\n" +
                comment(f"{w}Word is {article(spoken(n))} as the store spells it.") +
                f"type {w}Word struct{{ word string }}\n\n{visits}")

    def tests(self):
        s, a = self.s, self.a
        src = Source(a + "_test")
        src.need("encoding/json", "testing", s.module + "/" + s.dir + "/" + a, s.module + "/" + KIT + "domaintest")
        fields = self.record_fields()
        fixtures = [self.fixture(fields, 0)]
        if any(o for _, _, _, o, _ in fields):
            fixtures.append(self.fixture(fields, 1))
        listed = "".join(f"\t`{f}`,\n" for f in fixtures)
        timed = any(source == "closed" for *_, source in fields)
        x = s.primary
        roundtrip = "Store(r, x)" if timed else "Store(x)"
        src.add(comment("fixtures are stored records as the store writes them; each replays through Restore and Store "
                        "unchanged. Add the records a real store holds, written as json.Marshal writes them.") +
                f"var fixtures = []string{{\n{listed}}}\n")
        src.add("func TestFixturesReplayUnchanged(t *testing.T) {\n"
                "\tfor _, fixture := range fixtures {\n"
                f"\t\tvar r {a}.Record\n"
                "\t\tif err := json.Unmarshal([]byte(fixture), &r); err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n"
                f"\t\tx := domaintest.Ok(t, {a}.Restore(r))\n"
                f"\t\tgot, err := json.Marshal({a}.{roundtrip})\n"
                "\t\tif err != nil {\n\t\t\tt.Fatal(err)\n\t\t}\n"
                "\t\tif string(got) != fixture {\n\t\t\tt.Errorf(\"replayed\\n%s\\nas\\n%s\", fixture, got)\n\t\t}\n"
                "\t}\n}\n")
        refused = self.refused_record(fields)
        if refused:
            record, want = refused
            src.add(f"func TestRestoreRefusesWhatTheStoreCannotHold(t *testing.T) {{\n"
                    f"\tr := {a}.Record{{{record}}}\n"
                    f"\tgot := domaintest.Fails(t, {a}.Restore(r))\n"
                    f"\tif want := {go_string(chr(34) + want + chr(34))}; got != want {{\n"
                    '\t\tt.Errorf("got %q, want %q", got, want)\n\t}\n}\n')
        if s.act:
            src.need(s.module + "/" + KIT + "clock/clocktest", "time", s.module + "/" + s.dir)
            src.add(f"func TestActStoresTheDecision(t *testing.T) {{\n"
                    f"\tvar prev {a}.Record\n"
                    f"\tif err := json.Unmarshal([]byte(fixtures[0]), &prev); err != nil {{\n\t\tt.Fatal(err)\n\t}}\n"
                    "\tnow := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()\n"
                    f"\tdecided := domaintest.Ok(t, {s.name}.Decide(domaintest.Ok(t, {a}.Restore(prev)), "
                    f"{s.name}.CommandClose(), now))\n"
                    f"\tvar saved {a}.Record\n"
                    f"\tif err := {a}.Act(prev, decided, func(r {a}.Record) error {{ saved = r; return nil }}); "
                    "err != nil {\n\t\tt.Fatal(err)\n\t}\n"
                    '\tif saved.ClosedAt == nil || !saved.ClosedAt.Equal(time.Date(2026, 11, 22, 10, 0, 0, 0, '
                    'time.UTC)) {\n\t\tt.Errorf("saved %+v, want it closed at the clock\'s time", saved)\n\t}\n}\n')
        return src.text()

    def fixture(self, fields, variant):
        out = []
        for f, kind, key, omit, source in fields:
            if source == "closed":
                if variant:
                    out.append(f'"{key}":"2026-11-22T17:00:00+08:00"')
                continue
            if isinstance(source, Closed):
                word = source.cases[-1 if variant else 0][0]
                out.append(f'"{key}":"{word}"')
                continue
            if omit and not variant:
                continue
            if kind == "int":
                out.append(f'"{key}":{2 if variant else 1}')
            else:
                text = f"{spoken(source.field)} {2 if variant else 1}" if f != "ID" else f"{spoken(source.field)} 1"
                out.append(f'"{key}":"{text}"')
        return "{" + ",".join(out) + "}"

    def refused_record(self, fields):
        """A record Restore refuses and what it says, or None: an unknown word for a closed set, else an invalid
        part."""
        s = self.s
        words = [(f, source) for f, _, _, _, source in fields if isinstance(source, Closed)]
        if words and isinstance(s.primary, str):
            f0, c = words[0]
            values = []
            for f, kind, _, _, source in fields:
                if source == "closed":
                    continue
                if f == f0:
                    values.append(f'{f}: "from a newer peer"')
                elif isinstance(source, Closed):
                    values.append(f'{f}: "{source.cases[0][0]}"')
                elif kind == "int":
                    values.append(f"{f}: 1")
                else:
                    values.append(f'{f}: "{spoken(source.field)} 1"')
            return ", ".join(values), f"invalid {s.name}.{snake(s.entity)}.no_{snake(c.field)} {c.field}"
        for f, kind, _, _, source in fields:
            if isinstance(source, Value) and Writer(s).rule(source) is not None:
                values = []
                for g, kind2, _, _, other in fields:
                    if other == "closed" or g == f:
                        continue
                    if isinstance(other, Closed):
                        values.append(f'{g}: "{other.cases[0][0]}"')
                    elif kind2 == "int":
                        values.append(f"{g}: 1")
                    else:
                        values.append(f'{g}: "{spoken(other.field)} 1"')
                word = "empty" if kind == "string" else ("negative" if source.zero_is is not None else "not_positive")
                if word == "negative":
                    values.append(f"{f}: -1")
                return ", ".join(values), f"invalid {s.name}.{snake(source.field)}.{word} {source.field}"
        return None


# Writing.

def gofmt():
    found = shutil.which("gofmt")
    if found:
        return found
    go = shutil.which("go")
    if go:
        root = subprocess.run([go, "env", "GOROOT"], capture_output=True, text=True, timeout=60).stdout.strip()
        candidate = Path(root) / "bin" / "gofmt"
        if candidate.is_file():
            return str(candidate)
    raise Refused("gofmt is not on PATH: the scaffold formats what it writes with Go's own formatter")


def formatted(files):
    tool = gofmt()
    out = {}
    for name, text in files.items():
        done = subprocess.run([tool], input=text, capture_output=True, text=True, timeout=60)
        if done.returncode != 0:
            raise RuntimeError(f"gofmt refused {name}, a bug in the scaffold:\n{done.stderr}\n{text}")
        out[name] = done.stdout
    return out


def module_path(server):
    for line in (server / "go.mod").read_text(encoding="utf-8").splitlines():
        if line.startswith("module "):
            return line.split()[1]
    raise Refused(f"{server / 'go.mod'} names no module")


def parser():
    class ZeroIs(argparse.Action):
        def __call__(self, parser, namespace, value, option_string=None):
            values = getattr(namespace, "value", None) or []
            if not values:
                parser.error("--zero-is follows the --value it describes")
            zero = dict(getattr(namespace, "zero_is", None) or {})
            zero[len(values) - 1] = value
            namespace.zero_is = zero

    p = argparse.ArgumentParser(prog="new_domain_slice.py", description=__doc__.split("\n\n", 1)[0],
                                epilog=__doc__.split("\n\n", 1)[1], formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("platform", help="go (swift, kotlin and web come with their first domain module)")
    p.add_argument("dir", help="the package's directory under server/, starting internal/")
    p.add_argument("--doc", required=True, help="the one sentence after 'Package <name> '")
    p.add_argument("--shared", action="store_true")
    p.add_argument("--value", action="append", metavar="Name=string|int|part:Type,...")
    p.add_argument("--zero-is", action=ZeroIs, metavar="MEANING")
    p.add_argument("--parse", action="append", metavar="Name=raw:string,raw:int")
    p.add_argument("--closed", action="append", metavar="Name=a,b,c")
    p.add_argument("--entity", metavar="Name")
    p.add_argument("--id", metavar="NameID")
    p.add_argument("--builder", action="store_true")
    p.add_argument("--decider", action="store_true")
    p.add_argument("--adapter", action="store_true")
    p.add_argument("--act", action="store_true")
    p.add_argument("--root", type=Path, default=SERVER,
                   help="the tree to write under, laid out like server/ (default: this repository's server/)")
    return p


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    if argv and argv[0] in LATER:
        print(f"new_domain_slice.py: the {argv[0]} flavour arrives with the first {argv[0]} domain module (#10613, "
              f"item {LATER[argv[0]]}); only go is written today", file=sys.stderr)
        return 2
    args = parser().parse_args(argv)
    if args.platform != "go":
        print(f"new_domain_slice.py: {args.platform} is not a platform: go, swift, kotlin or web", file=sys.stderr)
        return 2
    try:
        s = Slice(args, module_path(SERVER))
        target = args.root / s.dir
        if target.exists():
            raise Refused(f"{target} exists: the scaffold starts a package, it never writes over one")
        files = formatted(Writer(s).files())
    except Refused as refused:
        print(f"new_domain_slice.py: {refused}", file=sys.stderr)
        return 2
    for name, text in sorted(files.items()):
        path = target / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
    shown = os.path.relpath(target, Path.cwd())
    print(f"new_domain_slice.py: wrote {len(files)} files under {shown}; run its rules and tests with "
          f"(cd server && go test ./{s.dir}/...), and classify the package"
          f"{' and its adapter' if s.adapter else ''} in schema/module-classification.txt")
    return 0


if __name__ == "__main__":
    sys.exit(main())
