// An independently compiled C consumer of the checked-in C header (#8560, #8912, #9017).
//
// Beyond the original sort smoke, this client replays the unchanged spec fixtures through the real C ABI:
// answer + display_answer (15 + 8 cases), stream_chunks (21), turns (57 static cases with the checked-in current-iOS
// overlay applied here, never in the core), task_index (5 complete expected objects plus the background receipt), and
// viewed_images (ordinary post-overlay plus source-derived folded expected objects).
// Expected objects are compared completely: both sides are parsed and re-serialized canonically (sorted object keys,
// compact separators), so object key order is insignificant while every value, array order and Unicode string is exact.
// Malformed inputs must return an empty buffer and a deliberately wrong expectation must be detected, per domain.
//
// This is Linux host C ABI evidence only, not Swift importer, Apple ABI, NAPI, device or performance evidence.
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "persona_core.h"

/* ---------- file and buffer helpers ---------- */

static void *read_file(const char *path, size_t *length) {
    FILE *file = fopen(path, "rb");
    if (!file) {
        fprintf(stderr, "open %s: %s\n", path, strerror(errno));
        return NULL;
    }
    if (fseek(file, 0, SEEK_END) != 0) {
        fclose(file);
        return NULL;
    }
    long end = ftell(file);
    if (end < 0 || fseek(file, 0, SEEK_SET) != 0) {
        fclose(file);
        return NULL;
    }
    size_t size = (size_t)end;
    unsigned char *data = malloc(size + 1);
    if (!data) {
        fclose(file);
        return NULL;
    }
    if (fread(data, 1, size, file) != size || ferror(file) || fclose(file) != 0) {
        free(data);
        return NULL;
    }
    data[size] = 0;
    *length = size;
    return data;
}

typedef struct {
    char *data;
    size_t length;
    size_t capacity;
    int failed;
} Buffer;

static void buf_reserve(Buffer *b, size_t extra) {
    if (b->failed) return;
    if (b->length + extra + 1 <= b->capacity) return;
    size_t capacity = b->capacity ? b->capacity : 64;
    while (capacity < b->length + extra + 1) capacity *= 2;
    char *data = realloc(b->data, capacity);
    if (!data) {
        b->failed = 1;
        return;
    }
    b->data = data;
    b->capacity = capacity;
}

static void buf_put(Buffer *b, const char *text, size_t length) {
    buf_reserve(b, length);
    if (b->failed) return;
    memcpy(b->data + b->length, text, length);
    b->length += length;
    b->data[b->length] = 0;
}

static void buf_str(Buffer *b, const char *text) { buf_put(b, text, strlen(text)); }
static void buf_char(Buffer *b, char c) { buf_put(b, &c, 1); }

static void buf_free(Buffer *b) {
    free(b->data);
    b->data = NULL;
    b->length = b->capacity = 0;
}

static char *jstrdup(const char *text) {
    size_t length = strlen(text);
    char *copy = malloc(length + 1);
    if (copy) memcpy(copy, text, length + 1);
    return copy;
}

/* ---------- JSON value model ---------- */

typedef enum { J_NULL, J_BOOL, J_NUMBER, J_STRING, J_ARRAY, J_OBJECT } JKind;

typedef struct JValue JValue;
typedef struct {
    char *key;
    JValue *value;
} JMember;

struct JValue {
    JKind kind;
    union {
        int boolean;
        struct { char *text; size_t length; } number; /* literal preserved byte for byte */
        struct { char *text; size_t length; } string; /* decoded UTF-8 */
        struct { JValue **items; size_t count; } array;
        struct { JMember *members; size_t count; } object;
    } u;
};

static void jfree(JValue *v) {
    if (!v) return;
    switch (v->kind) {
        case J_NUMBER:
        case J_STRING: free(v->u.string.text); break;
        case J_ARRAY:
            for (size_t i = 0; i < v->u.array.count; i++) jfree(v->u.array.items[i]);
            free(v->u.array.items);
            break;
        case J_OBJECT:
            for (size_t i = 0; i < v->u.object.count; i++) {
                free(v->u.object.members[i].key);
                jfree(v->u.object.members[i].value);
            }
            free(v->u.object.members);
            break;
        default: break;
    }
    free(v);
}

static JValue *jnew(JKind kind) {
    JValue *v = calloc(1, sizeof(*v));
    if (v) v->kind = kind;
    return v;
}

/* ---------- strict JSON parser (UTF-8 validated, \u escapes with surrogate pairs) ---------- */

typedef struct {
    const unsigned char *text;
    size_t length;
    size_t pos;
    int failed;
} Parser;

static void utf8_append(Buffer *b, unsigned cp) {
    char out[4];
    size_t n;
    if (cp < 0x80) { out[0] = (char)cp; n = 1; }
    else if (cp < 0x800) {
        out[0] = (char)(0xC0 | (cp >> 6));
        out[1] = (char)(0x80 | (cp & 0x3F));
        n = 2;
    } else if (cp < 0x10000) {
        out[0] = (char)(0xE0 | (cp >> 12));
        out[1] = (char)(0x80 | ((cp >> 6) & 0x3F));
        out[2] = (char)(0x80 | (cp & 0x3F));
        n = 3;
    } else {
        out[0] = (char)(0xF0 | (cp >> 18));
        out[1] = (char)(0x80 | ((cp >> 12) & 0x3F));
        out[2] = (char)(0x80 | ((cp >> 6) & 0x3F));
        out[3] = (char)(0x80 | (cp & 0x3F));
        n = 4;
    }
    buf_put(b, out, n);
}

/* Validates one UTF-8 sequence at p (length remaining); returns its byte length or 0 when invalid. */
static size_t utf8_sequence(const unsigned char *p, size_t length) {
    if (!length) return 0;
    unsigned char c = p[0];
    if (c < 0x80) return 1;
    size_t n;
    unsigned cp;
    if ((c & 0xE0) == 0xC0) { n = 2; cp = c & 0x1F; }
    else if ((c & 0xF0) == 0xE0) { n = 3; cp = c & 0x0F; }
    else if ((c & 0xF8) == 0xF0) { n = 4; cp = c & 0x07; }
    else return 0;
    if (length < n) return 0;
    for (size_t i = 1; i < n; i++) {
        if ((p[i] & 0xC0) != 0x80) return 0;
        cp = (cp << 6) | (p[i] & 0x3F);
    }
    if ((n == 2 && cp < 0x80) || (n == 3 && cp < 0x800) || (n == 4 && cp < 0x10000)) return 0;
    if (cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF)) return 0;
    return n;
}

static int hex4(Parser *p, unsigned *out) {
    if (p->pos + 4 > p->length) return 0;
    unsigned value = 0;
    for (int i = 0; i < 4; i++) {
        unsigned char c = p->text[p->pos++];
        value <<= 4;
        if (c >= '0' && c <= '9') value |= (unsigned)(c - '0');
        else if (c >= 'a' && c <= 'f') value |= (unsigned)(c - 'a' + 10);
        else if (c >= 'A' && c <= 'F') value |= (unsigned)(c - 'A' + 10);
        else return 0;
    }
    *out = value;
    return 1;
}

static int parse_string_raw(Parser *p, char **out, size_t *out_length) {
    if (p->pos >= p->length || p->text[p->pos] != '"') return 0;
    p->pos++;
    Buffer b = {0};
    while (p->pos < p->length) {
        unsigned char c = p->text[p->pos];
        if (c == '"') {
            p->pos++;
            if (b.failed) { buf_free(&b); return 0; }
            if (!b.data) { buf_reserve(&b, 0); if (b.failed) return 0; b.data[0] = 0; }
            *out = b.data;
            *out_length = b.length;
            return 1;
        }
        if (c == '\\') {
            p->pos++;
            if (p->pos >= p->length) break;
            unsigned char esc = p->text[p->pos++];
            switch (esc) {
                case '"': buf_char(&b, '"'); break;
                case '\\': buf_char(&b, '\\'); break;
                case '/': buf_char(&b, '/'); break;
                case 'b': buf_char(&b, '\b'); break;
                case 'f': buf_char(&b, '\f'); break;
                case 'n': buf_char(&b, '\n'); break;
                case 'r': buf_char(&b, '\r'); break;
                case 't': buf_char(&b, '\t'); break;
                case 'u': {
                    unsigned cp;
                    if (!hex4(p, &cp)) goto fail;
                    if (cp >= 0xD800 && cp <= 0xDBFF) {
                        if (p->pos + 2 > p->length || p->text[p->pos] != '\\' || p->text[p->pos + 1] != 'u') goto fail;
                        p->pos += 2;
                        unsigned low;
                        if (!hex4(p, &low) || low < 0xDC00 || low > 0xDFFF) goto fail;
                        cp = 0x10000 + ((cp - 0xD800) << 10) + (low - 0xDC00);
                    } else if (cp >= 0xDC00 && cp <= 0xDFFF) {
                        goto fail; /* unpaired low surrogate */
                    }
                    utf8_append(&b, cp);
                    break;
                }
                default: goto fail;
            }
            if (b.failed) goto fail;
            continue;
        }
        if (c < 0x20) goto fail; /* raw control characters are not valid JSON */
        size_t n = utf8_sequence(p->text + p->pos, p->length - p->pos);
        if (!n) goto fail;
        buf_put(&b, (const char *)(p->text + p->pos), n);
        p->pos += n;
        if (b.failed) goto fail;
        continue;
    fail:
        buf_free(&b);
        return 0;
    }
    buf_free(&b);
    return 0;
}

static void skip_ws(Parser *p) {
    while (p->pos < p->length) {
        unsigned char c = p->text[p->pos];
        if (c != ' ' && c != '\t' && c != '\n' && c != '\r') return;
        p->pos++;
    }
}

static JValue *parse_value(Parser *p, int depth);

static JValue *parse_number(Parser *p) {
    size_t start = p->pos;
    if (p->pos < p->length && p->text[p->pos] == '-') p->pos++;
    if (p->pos >= p->length) return NULL;
    if (p->text[p->pos] == '0') p->pos++;
    else if (p->text[p->pos] >= '1' && p->text[p->pos] <= '9') {
        while (p->pos < p->length && p->text[p->pos] >= '0' && p->text[p->pos] <= '9') p->pos++;
    } else return NULL;
    if (p->pos < p->length && p->text[p->pos] == '.') {
        p->pos++;
        if (p->pos >= p->length || p->text[p->pos] < '0' || p->text[p->pos] > '9') return NULL;
        while (p->pos < p->length && p->text[p->pos] >= '0' && p->text[p->pos] <= '9') p->pos++;
    }
    if (p->pos < p->length && (p->text[p->pos] == 'e' || p->text[p->pos] == 'E')) {
        p->pos++;
        if (p->pos < p->length && (p->text[p->pos] == '+' || p->text[p->pos] == '-')) p->pos++;
        if (p->pos >= p->length || p->text[p->pos] < '0' || p->text[p->pos] > '9') return NULL;
        while (p->pos < p->length && p->text[p->pos] >= '0' && p->text[p->pos] <= '9') p->pos++;
    }
    JValue *v = jnew(J_NUMBER);
    if (!v) return NULL;
    size_t length = p->pos - start;
    v->u.number.text = malloc(length + 1);
    if (!v->u.number.text) { jfree(v); return NULL; }
    memcpy(v->u.number.text, p->text + start, length);
    v->u.number.text[length] = 0;
    v->u.number.length = length;
    return v;
}

static JValue *parse_array(Parser *p, int depth) {
    p->pos++; /* '[' */
    JValue *v = jnew(J_ARRAY);
    if (!v) return NULL;
    skip_ws(p);
    if (p->pos < p->length && p->text[p->pos] == ']') { p->pos++; return v; }
    for (;;) {
        JValue *item = parse_value(p, depth + 1);
        if (!item) { jfree(v); return NULL; }
        JValue **items = realloc(v->u.array.items, (v->u.array.count + 1) * sizeof(*items));
        if (!items) { jfree(item); jfree(v); return NULL; }
        v->u.array.items = items;
        v->u.array.items[v->u.array.count++] = item;
        skip_ws(p);
        if (p->pos >= p->length) { jfree(v); return NULL; }
        if (p->text[p->pos] == ',') { p->pos++; continue; }
        if (p->text[p->pos] == ']') { p->pos++; return v; }
        jfree(v);
        return NULL;
    }
}

static JValue *parse_object(Parser *p, int depth) {
    p->pos++; /* '{' */
    JValue *v = jnew(J_OBJECT);
    if (!v) return NULL;
    skip_ws(p);
    if (p->pos < p->length && p->text[p->pos] == '}') { p->pos++; return v; }
    for (;;) {
        skip_ws(p);
        char *key = NULL;
        size_t key_length = 0;
        if (!parse_string_raw(p, &key, &key_length)) { jfree(v); return NULL; }
        (void)key_length;
        skip_ws(p);
        if (p->pos >= p->length || p->text[p->pos] != ':') { free(key); jfree(v); return NULL; }
        p->pos++;
        JValue *member = parse_value(p, depth + 1);
        if (!member) { free(key); jfree(v); return NULL; }
        JMember *members = realloc(v->u.object.members, (v->u.object.count + 1) * sizeof(*members));
        if (!members) { free(key); jfree(member); jfree(v); return NULL; }
        v->u.object.members = members;
        v->u.object.members[v->u.object.count].key = key;
        v->u.object.members[v->u.object.count].value = member;
        v->u.object.count++;
        skip_ws(p);
        if (p->pos >= p->length) { jfree(v); return NULL; }
        if (p->text[p->pos] == ',') { p->pos++; continue; }
        if (p->text[p->pos] == '}') { p->pos++; return v; }
        jfree(v);
        return NULL;
    }
}

static JValue *parse_value(Parser *p, int depth) {
    if (depth > 200) return NULL;
    skip_ws(p);
    if (p->pos >= p->length) return NULL;
    unsigned char c = p->text[p->pos];
    if (c == '{') return parse_object(p, depth);
    if (c == '[') return parse_array(p, depth);
    if (c == '"') {
        JValue *v = jnew(J_STRING);
        if (!v) return NULL;
        if (!parse_string_raw(p, &v->u.string.text, &v->u.string.length)) { jfree(v); return NULL; }
        return v;
    }
    if (c == 't' && p->pos + 4 <= p->length && memcmp(p->text + p->pos, "true", 4) == 0) {
        p->pos += 4;
        JValue *v = jnew(J_BOOL);
        if (v) v->u.boolean = 1;
        return v;
    }
    if (c == 'f' && p->pos + 5 <= p->length && memcmp(p->text + p->pos, "false", 5) == 0) {
        p->pos += 5;
        return jnew(J_BOOL);
    }
    if (c == 'n' && p->pos + 4 <= p->length && memcmp(p->text + p->pos, "null", 4) == 0) {
        p->pos += 4;
        return jnew(J_NULL);
    }
    return parse_number(p);
}

static JValue *json_parse(const void *text, size_t length) {
    Parser p = { text, length, 0, 0 };
    JValue *v = parse_value(&p, 0);
    if (!v) return NULL;
    skip_ws(&p);
    if (p.pos != p.length) { jfree(v); return NULL; }
    return v;
}

/* ---------- accessors ---------- */

static JValue *jget(const JValue *v, const char *key) {
    if (!v || v->kind != J_OBJECT) return NULL;
    for (size_t i = 0; i < v->u.object.count; i++) {
        if (strcmp(v->u.object.members[i].key, key) == 0) return v->u.object.members[i].value;
    }
    return NULL;
}

static const char *jstr(const JValue *v) { return v && v->kind == J_STRING ? v->u.string.text : NULL; }

static int jbool(const JValue *v, int *out) {
    if (!v || v->kind != J_BOOL) return 0;
    *out = v->u.boolean;
    return 1;
}

static int jint(const JValue *v, long *out) {
    if (!v || v->kind != J_NUMBER) return 0;
    char *end = NULL;
    errno = 0;
    long value = strtol(v->u.number.text, &end, 10);
    if (errno || !end || *end) return 0;
    *out = value;
    return 1;
}

static size_t jcount(const JValue *v) {
    if (!v) return 0;
    if (v->kind == J_ARRAY) return v->u.array.count;
    if (v->kind == J_OBJECT) return v->u.object.count;
    return 0;
}

static JValue *jat(const JValue *v, size_t index) {
    return v && v->kind == J_ARRAY && index < v->u.array.count ? v->u.array.items[index] : NULL;
}

/* ---------- builders (for the overlay and the conditional expected shapes) ---------- */

static JValue *jstring(const char *text) {
    JValue *v = jnew(J_STRING);
    if (!v) return NULL;
    size_t length = strlen(text);
    v->u.string.text = malloc(length + 1);
    if (!v->u.string.text) { jfree(v); return NULL; }
    memcpy(v->u.string.text, text, length + 1);
    v->u.string.length = length;
    return v;
}

static JValue *jbool_value(int value) {
    JValue *v = jnew(J_BOOL);
    if (v) v->u.boolean = value;
    return v;
}

static JValue *jnumber(long value) {
    char text[32];
    snprintf(text, sizeof(text), "%ld", value);
    JValue *v = jnew(J_NUMBER);
    if (!v) return NULL;
    size_t length = strlen(text);
    v->u.number.text = malloc(length + 1);
    if (!v->u.number.text) { jfree(v); return NULL; }
    memcpy(v->u.number.text, text, length + 1);
    v->u.number.length = length;
    return v;
}

static void jpush(JValue *array, JValue *value) {
    if (!array || array->kind != J_ARRAY || !value) { jfree(value); return; }
    JValue **items = realloc(array->u.array.items, (array->u.array.count + 1) * sizeof(*items));
    if (!items) { jfree(value); return; }
    array->u.array.items = items;
    array->u.array.items[array->u.array.count++] = value;
}

static JValue *jclone(const JValue *v) {
    if (!v) return NULL;
    JValue *copy = jnew(v->kind);
    if (!copy) return NULL;
    switch (v->kind) {
        case J_BOOL: copy->u.boolean = v->u.boolean; return copy;
        case J_NUMBER: {
            copy->u.number.text = malloc(v->u.number.length + 1);
            if (!copy->u.number.text) { jfree(copy); return NULL; }
            memcpy(copy->u.number.text, v->u.number.text, v->u.number.length + 1);
            copy->u.number.length = v->u.number.length;
            return copy;
        }
        case J_STRING: {
            copy->u.string.text = malloc(v->u.string.length + 1);
            if (!copy->u.string.text) { jfree(copy); return NULL; }
            memcpy(copy->u.string.text, v->u.string.text, v->u.string.length + 1);
            copy->u.string.length = v->u.string.length;
            return copy;
        }
        case J_ARRAY:
            for (size_t i = 0; i < v->u.array.count; i++) {
                JValue *item = jclone(v->u.array.items[i]);
                if (!item) { jfree(copy); return NULL; }
                JValue **items = realloc(copy->u.array.items, (copy->u.array.count + 1) * sizeof(*items));
                if (!items) { jfree(item); jfree(copy); return NULL; }
                copy->u.array.items = items;
                copy->u.array.items[copy->u.array.count++] = item;
            }
            return copy;
        case J_OBJECT:
            for (size_t i = 0; i < v->u.object.count; i++) {
                JValue *member = jclone(v->u.object.members[i].value);
                char *key = jstrdup(v->u.object.members[i].key);
                if (!member || !key) { jfree(member); free(key); jfree(copy); return NULL; }
                JMember *members = realloc(copy->u.object.members, (copy->u.object.count + 1) * sizeof(*members));
                if (!members) { free(key); jfree(member); jfree(copy); return NULL; }
                copy->u.object.members = members;
                copy->u.object.members[copy->u.object.count].key = key;
                copy->u.object.members[copy->u.object.count].value = member;
                copy->u.object.count++;
            }
            return copy;
        default: return copy;
    }
}

/* Replaces the member when the key exists, otherwise appends it. */
static int jset(JValue *object, const char *key, JValue *value) {
    if (!object || object->kind != J_OBJECT || !value) { jfree(value); return 0; }
    for (size_t i = 0; i < object->u.object.count; i++) {
        if (strcmp(object->u.object.members[i].key, key) == 0) {
            jfree(object->u.object.members[i].value);
            object->u.object.members[i].value = value;
            return 1;
        }
    }
    char *copy = jstrdup(key);
    if (!copy) { jfree(value); return 0; }
    JMember *members = realloc(object->u.object.members, (object->u.object.count + 1) * sizeof(*members));
    if (!members) { free(copy); jfree(value); return 0; }
    object->u.object.members = members;
    object->u.object.members[object->u.object.count].key = copy;
    object->u.object.members[object->u.object.count].value = value;
    object->u.object.count++;
    return 1;
}

/* ---------- canonical serialization: sorted object keys, compact separators ---------- */

static void write_json_string(Buffer *b, const char *text, size_t length) {
    buf_char(b, '"');
    size_t i = 0;
    while (i < length) {
        unsigned char c = (unsigned char)text[i];
        if (c == '"') { buf_str(b, "\\\""); i++; }
        else if (c == '\\') { buf_str(b, "\\\\"); i++; }
        else if (c == '\b') { buf_str(b, "\\b"); i++; }
        else if (c == '\f') { buf_str(b, "\\f"); i++; }
        else if (c == '\n') { buf_str(b, "\\n"); i++; }
        else if (c == '\r') { buf_str(b, "\\r"); i++; }
        else if (c == '\t') { buf_str(b, "\\t"); i++; }
        else if (c < 0x20) {
            char escape[7];
            snprintf(escape, sizeof(escape), "\\u%04x", c);
            buf_str(b, escape);
            i++;
        } else {
            size_t n = utf8_sequence((const unsigned char *)text + i, length - i);
            if (!n) { b->failed = 1; return; }
            buf_put(b, text + i, n);
            i += n;
        }
    }
    buf_char(b, '"');
}

static int member_compare(const void *a, const void *b) {
    const JMember *ma = a, *mb = b;
    return strcmp(ma->key, mb->key);
}

static void write_canonical(Buffer *b, const JValue *v) {
    if (b->failed) return;
    switch (v->kind) {
        case J_NULL: buf_str(b, "null"); return;
        case J_BOOL: buf_str(b, v->u.boolean ? "true" : "false"); return;
        case J_NUMBER: buf_put(b, v->u.number.text, v->u.number.length); return;
        case J_STRING: write_json_string(b, v->u.string.text, v->u.string.length); return;
        case J_ARRAY:
            buf_char(b, '[');
            for (size_t i = 0; i < v->u.array.count; i++) {
                if (i) buf_char(b, ',');
                write_canonical(b, v->u.array.items[i]);
            }
            buf_char(b, ']');
            return;
        case J_OBJECT: {
            buf_char(b, '{');
            JMember *sorted = NULL;
            if (v->u.object.count) {
                sorted = malloc(v->u.object.count * sizeof(*sorted));
                if (!sorted) { b->failed = 1; return; }
                memcpy(sorted, v->u.object.members, v->u.object.count * sizeof(*sorted));
                qsort(sorted, v->u.object.count, sizeof(*sorted), member_compare);
            }
            for (size_t i = 0; i < v->u.object.count; i++) {
                if (i) buf_char(b, ',');
                write_json_string(b, sorted[i].key, strlen(sorted[i].key));
                buf_char(b, ':');
                write_canonical(b, sorted[i].value);
            }
            free(sorted);
            buf_char(b, '}');
            return;
        }
    }
}

/* Deep semantic equality: object key order is insignificant, array order and every scalar are exact. */
static int jequal(const JValue *a, const JValue *b) {
    Buffer ba = {0}, bb = {0};
    write_canonical(&ba, a);
    write_canonical(&bb, b);
    int equal = !ba.failed && !bb.failed && ba.length == bb.length
        && (ba.length == 0 || memcmp(ba.data, bb.data, ba.length) == 0);
    buf_free(&ba);
    buf_free(&bb);
    return equal;
}

/* ---------- replay plumbing ---------- */

static int failures = 0;

static void fail(const char *domain, const char *name, const char *why) {
    fprintf(stderr, "FAIL %s/%s: %s\n", domain, name, why);
    failures++;
}

/* Exact-name manifest assertion, the same boundary the Kotlin replays assert: a case added, dropped or renamed in the
 * fixture without updating this client is a failure, never a silent skip. */
static int manifest_has(const char *const *manifest, size_t count, const char *name) {
    for (size_t i = 0; i < count; i++) {
        if (strcmp(manifest[i], name) == 0) return 1;
    }
    return 0;
}

typedef PersonaCoreBytes (*Projection)(const uint8_t *, size_t);

/* Runs one projection on the case's `input` member and returns the parsed result (NULL on empty output). */
static JValue *project_case(Projection projection, const JValue *input) {
    if (!input) return NULL;
    Buffer encoded = {0};
    write_canonical(&encoded, input); /* re-encoding is semantics-preserving: the core parses JSON */
    if (encoded.failed) { buf_free(&encoded); return NULL; }
    PersonaCoreBytes result = projection((const uint8_t *)encoded.data, encoded.length);
    JValue *parsed = NULL;
    if (result.data && result.length) parsed = json_parse(result.data, result.length);
    persona_core_bytes_free(result);
    buf_free(&encoded);
    return parsed;
}

static JValue *load_json(const char *path) {
    size_t length = 0;
    void *text = read_file(path, &length);
    if (!text) return NULL;
    JValue *v = json_parse(text, length);
    free(text);
    return v;
}

static void expect_empty(Projection projection, const char *domain, const char *label, const char *input) {
    PersonaCoreBytes result = projection((const uint8_t *)input, strlen(input));
    if (result.data || result.length || result.capacity) fail(domain, label, "malformed input did not return an empty buffer");
    persona_core_bytes_free(result);
}

/* ---------- sort (the original #8560 smoke, now semantic) ---------- */

static int check_sort(const char *fixtures) {
    char path[4096];
    snprintf(path, sizeof(path), "%s/sort/filters.json", fixtures);
    JValue *doc = load_json(path);
    JValue *cases = jget(doc, "cases");
    const JValue *found = NULL;
    for (size_t i = 0; i < jcount(cases); i++) {
        const JValue *c = jat(cases, i);
        if (jstr(jget(c, "name")) && strcmp(jstr(jget(c, "name")), "empty_list") == 0) found = c;
    }
    if (!found) { fail("sort", "empty_list", "case not found"); jfree(doc); return 1; }
    JValue *actual = project_case(persona_core_project_sort, jget(found, "input"));
    if (!actual || !jequal(actual, jget(found, "expected"))) fail("sort", "empty_list", "projection differs from the fixture");
    jfree(actual);
    jfree(doc);
    expect_empty(persona_core_project_sort, "sort", "wrong_schema", "{\"invalid\":true}");
    expect_empty(persona_core_project_sort, "sort", "invalid_json", "{\"sessions\":[}");
    expect_empty(persona_core_project_sort, "sort", "empty", "");
    return 0;
}

/* ---------- stream_chunks ---------- */

static const char *const stream_manifest[] = {
    "blank_line_at_end_is_not_cut_yet",
    "carriage_return_is_not_a_line_separator",
    "consecutive_blank_lines_keep_their_newline",
    "crlf_carriage_return_is_not_a_blank_line",
    "crlf_fence_is_not_recognized",
    "cuts_after_blank_line_once_next_block_starts",
    "empty_input",
    "indented_fence_preserves_original_spaces",
    "inline_backticks_do_not_open_a_fence",
    "keeps_indented_continuation_with_block",
    "language_suffixed_line_does_not_close_fence",
    "leading_blank_line_can_finish_an_empty_block",
    "longer_fence_ignores_shorter_inner_closer",
    "never_cuts_inside_code_fence",
    "non_ascii_space_is_not_raw_indentation",
    "prewarm_uses_the_tail",
    "reassembles_full_text",
    "space_padded_separator_is_omitted",
    "tab_indented_continuation_stays_with_block",
    "tilde_fence_requires_tilde_closer",
    "unclosed_fence_keeps_remainder_in_tail",
};

static void check_stream(const char *fixtures) {
    char path[4096];
    snprintf(path, sizeof(path), "%s/stream-chunks/split.json", fixtures);
    JValue *doc = load_json(path);
    if (!doc) { fail("stream_chunks", "split.json", "cannot load fixture"); return; }
    JValue *cases = jget(doc, "cases");
    size_t total = jcount(cases);
    if (total != 21) fail("stream_chunks", "manifest", "expected exactly 21 cases");
    size_t replayed = 0;
    for (size_t i = 0; i < total; i++) {
        const JValue *c = jat(cases, i);
        const char *name = jstr(jget(c, "name"));
        if (!name) { fail("stream_chunks", "case", "missing name"); continue; }
        if (!manifest_has(stream_manifest, 21, name)) fail("stream_chunks", name, "case is not in the exact manifest");
        JValue *actual = project_case(persona_core_project_stream_chunks, jget(c, "input"));
        if (!actual) { fail("stream_chunks", name, "empty projection"); continue; }
        if (!jequal(actual, jget(c, "expected"))) {
            fail("stream_chunks", name, "complete {done, tail} object differs from the unchanged fixture");
        } else {
            replayed++;
        }
        /* A deliberately wrong expectation must be detected by this same comparison. */
        if (i == 0) {
            JValue *wrong = jnew(J_OBJECT);
            jset(wrong, "done", jnew(J_ARRAY));
            jset(wrong, "tail", jstring("wrong"));
            if (jequal(actual, wrong)) fail("stream_chunks", name, "a wrong expected object compared equal");
            jfree(wrong);
        }
        jfree(actual);
    }
    if (replayed != 21) fail("stream_chunks", "manifest", "not all 21 cases matched");
    expect_empty(persona_core_project_stream_chunks, "stream_chunks", "wrong_schema", "{\"invalid\":true}");
    expect_empty(persona_core_project_stream_chunks, "stream_chunks", "invalid_json", "not JSON");
    jfree(doc);
    printf("c-abi: stream_chunks %zu/21 complete expected objects\n", replayed);
}

/* ---------- answer + display_answer ---------- */

static const char *const question_manifest[] = {
    "all_answered_can_submit_from_earlier_page", "blank_other_cannot_submit", "blank_other_uses_picks",
    "empty_answer_cannot_submit", "last_page_missing_answer_returns_to_gap", "multiple_other_overrides_picks",
    "multiple_pick_toggles_off", "multiple_pick_waits_for_submit", "multiple_picks_follow_option_order",
    "multiple_questions_send_original_keys", "next_page_does_not_send", "other_answer_trimmed",
    "sending_disables_option", "single_pick_clears_other", "single_tap_sends_original_label",
};

static const char *const recommended_manifest[] = {
    "recommended_case_sensitive", "recommended_chinese_ascii", "recommended_chinese_fullwidth",
    "recommended_suffix_only", "recommended_spaced", "recommended_trailing_space",
    "recommended_trims_prefix_space", "recommended_unspaced",
};

/* Builds the fixture's conditional expected shape from the full transition, mirroring the Kotlin replay. */
static JValue *answer_expected_shape(const JValue *c, const JValue *transition, int recommended) {
    const JValue *input = jget(c, "input");
    const JValue *operation = jget(input, "operation");
    const JValue *interaction = jget(input, "interaction");
    if (!operation || !interaction) return NULL;
    long page = 0, actual_page = 0;
    if (!jint(jget(operation, "page"), &page) || page < 0 || !jint(jget(transition, "page"), &actual_page)) return NULL;
    JValue *shape = jnew(J_OBJECT);
    if (!shape) return NULL;
    jset(shape, "allowed", jclone(jget(transition, "allowed")));
    jset(shape, "resolution", jclone(jget(transition, "resolution")));
    if (actual_page != page) jset(shape, "next_page", jclone(jget(transition, "page")));
    const JValue *question = jat(jget(jget(input, "interaction"), "questions"), (size_t)page);
    int multi = 0;
    jbool(jget(question, "multi_select"), &multi);
    int allowed = 0;
    jbool(jget(transition, "allowed"), &allowed);
    const char *action = jstr(jget(operation, "action"));
    if (action && strcmp(action, "pick_option") == 0 && allowed && multi) {
        jset(shape, "picks", jclone(jget(transition, "picks")));
    }
    if (recommended) {
        long option_index = -1;
        if (!jint(jget(operation, "option_index"), &option_index)) { jfree(shape); return NULL; }
        const JValue *option = jat(jget(question, "options"), (size_t)option_index);
        const char *label = jstr(jget(option, "label"));
        if (!label) { jfree(shape); return NULL; }
        /* display_answer takes a bare JSON string, not an object. */
        Buffer encoded = {0};
        write_json_string(&encoded, label, strlen(label));
        PersonaCoreBytes result = persona_core_display_answer((const uint8_t *)encoded.data, encoded.length);
        buf_free(&encoded);
        JValue *display = NULL;
        if (result.data && result.length) display = json_parse(result.data, result.length);
        persona_core_bytes_free(result);
        if (!display) { jfree(shape); return NULL; }
        jset(shape, "display", display);
    }
    return shape;
}

static void check_answer_file(const char *fixtures, const char *file, int recommended, size_t expected_count) {
    char path[4096];
    snprintf(path, sizeof(path), "%s/answer/%s", fixtures, file);
    JValue *doc = load_json(path);
    if (!doc) { fail("answer", file, "cannot load fixture"); return; }
    JValue *cases = jget(doc, "cases");
    if (jcount(cases) != expected_count) fail("answer", file, "unexpected case count");
    size_t replayed = 0;
    const JValue *first = NULL;
    for (size_t i = 0; i < jcount(cases); i++) {
        const JValue *c = jat(cases, i);
        const char *name = jstr(jget(c, "name"));
        if (!name) continue;
        if (!manifest_has(recommended ? recommended_manifest : question_manifest, expected_count, name)) {
            fail("answer", name, "case is not in the exact manifest");
            continue;
        }
        if (strcmp(name, "single_tap_sends_original_label") == 0) first = c;
        JValue *transition = project_case(persona_core_project_answer, jget(c, "input"));
        if (!transition) { fail("answer", name, "empty transition"); continue; }
        JValue *shape = answer_expected_shape(c, transition, recommended);
        if (!shape) { fail("answer", name, "cannot build the expected shape"); jfree(transition); continue; }
        if (!jequal(shape, jget(c, "expected"))) {
            fail("answer", name, "complete expected object differs from the unchanged fixture");
        } else {
            replayed++;
        }
        jfree(shape);
        jfree(transition);
    }
    if (replayed != expected_count) fail("answer", file, "not every case matched");
    printf("c-abi: answer %s %zu/%zu complete expected objects\n", file, replayed, expected_count);

    if (!recommended && first) {
        /* Invalid page and out-of-range option must return an empty buffer, not a transition. */
        for (int variant = 0; variant < 2; variant++) {
            JValue *input = jclone(jget(first, "input"));
            JValue *operation = jget(input, "operation");
            JValue *number = jnew(J_NUMBER);
            const char *literal = "99";
            number->u.number.text = jstrdup(literal);
            number->u.number.length = 2;
            jset(operation, variant == 0 ? "page" : "option_index", number);
            Buffer encoded = {0};
            write_canonical(&encoded, input);
            PersonaCoreBytes result = persona_core_project_answer((const uint8_t *)encoded.data, encoded.length);
            if (result.data || result.length) fail("answer", variant == 0 ? "invalid_page" : "invalid_option", "did not return an empty buffer");
            persona_core_bytes_free(result);
            buf_free(&encoded);
            jfree(input);
        }
        /* A deliberately wrong expectation must be detected by the complete-object comparison. */
        JValue *transition = project_case(persona_core_project_answer, jget(first, "input"));
        JValue *shape = answer_expected_shape(first, transition, 0);
        JValue *wrong = jnew(J_OBJECT);
        jset(wrong, "allowed", jbool_value(0));
        jset(wrong, "resolution", jnew(J_NULL));
        if (shape && jequal(shape, wrong)) fail("answer", "wrong_expectation", "a wrong expected object compared equal");
        jfree(wrong);
        jfree(shape);
        jfree(transition);
    }
    jfree(doc);
}

static void check_answer_boundaries(void) {
    /* U+200B is not Apple whitespace: a zero-width-space answer stays non-blank where the fixtures say so, and the
     * display trim keeps the trailing newline. Both expectations are asserted literally, not derived from the core. */
    const char *submit = "{\"interaction\":{\"kind\":\"question\",\"questions\":[{\"question\":\"One\","
                         "\"options\":[{\"label\":\"Yes\"}]}]},\"operation\":{\"action\":\"submit\",\"page\":0,"
                         "\"picks\":{},\"other\":{\"One\":\" \xe2\x80\x8b \"}}}";
    PersonaCoreBytes result = persona_core_project_answer((const uint8_t *)submit, strlen(submit));
    JValue *transition = result.data && result.length ? json_parse(result.data, result.length) : NULL;
    persona_core_bytes_free(result);
    if (!transition) { fail("answer", "u200b", "no transition"); return; }
    int allowed = 1;
    jbool(jget(transition, "allowed"), &allowed);
    if (allowed) fail("answer", "u200b", "a zero-width-space-only other answer must not submit");
    jfree(transition);

    const char *label = "\"\xe2\x80\x8b A\\n (Recommended)\"";
    result = persona_core_display_answer((const uint8_t *)label, strlen(label));
    JValue *display = result.data && result.length ? json_parse(result.data, result.length) : NULL;
    persona_core_bytes_free(result);
    JValue *expected = jnew(J_OBJECT);
    jset(expected, "text", jstring("A\n"));
    jset(expected, "recommended", jbool_value(1));
    if (!display || !jequal(display, expected)) fail("answer", "display_u200b", "display trim/recommendation boundary differs");
    jfree(display);
    jfree(expected);
    expect_empty(persona_core_project_answer, "answer", "wrong_schema", "{\"invalid\":true}");
    expect_empty(persona_core_project_answer, "answer", "invalid_json", "not JSON");
    expect_empty(persona_core_display_answer, "answer", "display_not_a_string", "{\"label\":1}");
}

/* ---------- turns (static cases + the checked-in current-iOS overlay) ---------- */

static int wake_pending(const JValue *wake, const char *name, const char *turn_id) {
    const JValue *pending = jget(wake, "pending");
    for (size_t i = 0; i < jcount(pending); i++) {
        const JValue *reference = jat(pending, i);
        const char *c = jstr(jget(reference, "case"));
        const char *t = jstr(jget(reference, "turn"));
        if (c && t && strcmp(c, name) == 0 && strcmp(t, turn_id) == 0) return 1;
    }
    return 0;
}

/* Applies the overlay exactly as the Kotlin replay does: drop the stale wake key, set has_pending_wake per the
 * overlay's pending references, then apply each case entry's `replace` and `turn_fields`. */
static JValue *expected_turns(const JValue *c, const JValue *overlay, int apply_case_patch, int *ok) {
    const char *name = jstr(jget(c, "name"));
    const JValue *wake = jget(overlay, "turn_wake_state");
    const char *drop = jstr(jget(wake, "drop"));
    const char *set = jstr(jget(wake, "set"));
    *ok = 0;
    JValue *expected = jclone(jget(c, "expected"));
    if (!expected || !drop || !set) { jfree(expected); return NULL; }
    JValue *turns = jget(expected, "turns");
    for (size_t i = 0; i < jcount(turns); i++) {
        JValue *turn = turns->u.array.items[i];
        const char *id = jstr(jget(turn, "id"));
        int found = 0;
        for (size_t m = 0; m < turn->u.object.count; m++) {
            if (strcmp(turn->u.object.members[m].key, drop) == 0) {
                free(turn->u.object.members[m].key);
                jfree(turn->u.object.members[m].value);
                memmove(&turn->u.object.members[m], &turn->u.object.members[m + 1],
                        (turn->u.object.count - m - 1) * sizeof(JMember));
                turn->u.object.count--;
                found = 1;
                break;
            }
        }
        if (!found || !id) { fail("turns", name ? name : "?", "stale wake drop key missing"); jfree(expected); return NULL; }
        if (jget(turn, set)) { fail("turns", name, "turn already carries the wake set key"); jfree(expected); return NULL; }
        jset(turn, set, jbool_value(wake_pending(wake, name, id)));
    }
    if (apply_case_patch) {
        const JValue *entries = jget(overlay, "cases");
        for (size_t i = 0; i < jcount(entries); i++) {
            const JValue *entry = jat(entries, i);
            const char *entry_name = jstr(jget(entry, "name"));
            if (!entry_name || strcmp(entry_name, name) != 0) continue;
            const JValue *replace = jget(entry, "replace");
            for (size_t m = 0; m < jcount(replace); m++) {
                jset(expected, replace->u.object.members[m].key, jclone(replace->u.object.members[m].value));
            }
            /* `replace` may have replaced the whole turns array; re-read it before applying turn fields. */
            turns = jget(expected, "turns");
            const JValue *fields = jget(entry, "turn_fields");
            for (size_t m = 0; m < jcount(fields); m++) {
                const char *id = fields->u.object.members[m].key;
                const JValue *patch = fields->u.object.members[m].value;
                size_t matched = 0;
                for (size_t t = 0; t < jcount(turns); t++) {
                    JValue *turn = turns->u.array.items[t];
                    const char *turn_id = jstr(jget(turn, "id"));
                    if (turn_id && strcmp(turn_id, id) == 0) {
                        matched++;
                        for (size_t f = 0; f < jcount(patch); f++) {
                            jset(turn, patch->u.object.members[f].key, jclone(patch->u.object.members[f].value));
                        }
                    }
                }
                if (matched != 1) { fail("turns", name, "overlay must name exactly one turn"); jfree(expected); return NULL; }
            }
        }
    }
    *ok = 1;
    JValue *result = jclone(turns);
    jfree(expected);
    return result;
}

/* The exact 57 static cases, the same manifest the Kotlin replay asserts. The four operation-sequence cases are the
 * only corpus members excluded here; three of the 57 compare empty turn arrays and are not turn-object coverage. */
static const char *const turns_manifest[] = {
    "absorbed_user_images", "background_running_badge", "closed_work_identity",
    "compaction_stays_inside_work", "empty_session", "empty_thinking_between_tools_is_retained",
    "failed_tool_counts_known_failure", "fold_counts_tools_and_thoughts", "frozen_snapshot_excludes_newer_items",
    "frozen_uses_numeric_block_order", "interrupt_is_trailing_notice", "last_result_for_call_wins",
    "late_result_attaches_to_original_call", "live_answer_after_wake", "live_empty_work",
    "live_queued_message_keeps_position", "live_reply_stays_after_work", "live_tail_runs_behind_chip",
    "long_answer_after_wake", "menu_owner_per_message", "missing_start_time_hides_duration",
    "missing_start_time_uses_first_step", "missing_tool_result_is_running", "monitor_event_uses_first_detail_line",
    "multiple_interactions_in_footer", "multiple_wakes_pick_latest_answer", "notification_without_wake",
    "older_page_stays_visible_during_freeze", "only_last_reply_in_each_wake_is_candidate", "open_queued_messages_work",
    "open_work_identity", "orphan_assistant", "orphan_tool_result_is_not_a_row",
    "orphan_wake_opens_with_notice", "ordinary_user_starts_next_turn", "photos_after_wake_stay_with_message",
    "plain_exchange", "queued_without_existing_turn", "recap_separates_turns",
    "regular_user_images", "short_reply_after_wake_is_the_answer", "single_interaction_in_footer",
    "standalone_empty_thinking_hidden", "task_notices_find_calls", "task_status_outcomes",
    "task_title_containing_interrupt_is_not_interrupt", "task_without_loaded_call_uses_title", "thinking_before_between_after_tools",
    "thinking_narration_preserved", "three_users_apart_without_steps", "timed_work_ends_at_answer",
    "tool_images_belong_to_call", "trailing_tool_after_answer", "unknown_item_is_ignored",
    "wake_after_recap_reopens_turn", "wake_without_any_reply", "finished_queued_messages_apart",
};

static int turn_empty_only(const char *name) {
    return strcmp(name, "empty_session") == 0 || strcmp(name, "single_interaction_in_footer") == 0
        || strcmp(name, "multiple_interactions_in_footer") == 0;
}

static void check_turns(const char *fixtures) {
    char path[4096];
    snprintf(path, sizeof(path), "%s/lines/ios/differences.json", fixtures);
    JValue *overlay = load_json(path);
    if (!overlay) { fail("turns", "overlay", "the checked-in current-iOS overlay is required"); return; }
    static const char *const files[] = {
        "folds.json", "frozen.json", "layout.json", "queued.json", "rebuild.json",
        "structure.json", "tasks.json", "wakes.json", "work.json",
    };
    size_t replayed = 0, turn_objects = 0, corpus = 0;
    JValue *plain_input = NULL; /* cloned: the fixture documents are freed after each file */
    for (size_t f = 0; f < sizeof(files) / sizeof(files[0]); f++) {
        snprintf(path, sizeof(path), "%s/lines/%s", fixtures, files[f]);
        JValue *doc = load_json(path);
        if (!doc) { fail("turns", files[f], "cannot load fixture"); continue; }
        JValue *cases = jget(doc, "cases");
        for (size_t i = 0; i < jcount(cases); i++) {
            const JValue *c = jat(cases, i);
            const char *name = jstr(jget(c, "name"));
            if (!name) continue;
            corpus++;
            const JValue *expected = jget(c, "expected");
            const JValue *input = jget(c, "input");
            if (jget(expected, "checkpoints") || jget(input, "operations")) continue; /* operation cases */
            if (!input) { fail("turns", name, "case has no input object"); continue; }
            if (!manifest_has(turns_manifest, 57, name)) {
                fail("turns", name, "static case is not in the exact 57-case manifest");
                continue;
            }
            if (strcmp(name, "plain_exchange") == 0) {
                jfree(plain_input);
                plain_input = jclone(input);
            }
            JValue *actual = project_case(persona_core_project_turns, jget(c, "input"));
            if (!actual) { fail("turns", name, "empty projection"); continue; }
            int ok = 0;
            JValue *wanted = expected_turns(c, overlay, 1, &ok);
            if (!ok) { jfree(actual); continue; }
            if (!jequal(actual, wanted)) {
                fail("turns", name, "complete turns array differs from the fixture plus the current-iOS overlay");
            } else {
                replayed++;
                if (turn_empty_only(name)) {
                    if (jcount(wanted) != 0) fail("turns", name, "an empty-array case gained turns; it is not layout coverage");
                } else {
                    if (jcount(wanted) == 0) fail("turns", name, "a non-empty case has no turn-grouping coverage");
                    turn_objects += jcount(wanted);
                }
            }
            jfree(wanted);
            jfree(actual);
        }
        jfree(doc);
    }
    if (corpus != 61) fail("turns", "manifest", "the corpus must hold exactly 61 cases including the four operation ones");
    if (replayed != 57) fail("turns", "manifest", "exactly 57 static cases must replay");
    if (turn_objects != 62) fail("turns", "manifest", "the 54 non-empty cases must hold 62 complete turn objects");
    printf("c-abi: turns %zu/57 static cases, %zu complete turn objects, overlay applied client-side\n", replayed, turn_objects);

    if (plain_input) {
        JValue *actual = project_case(persona_core_project_turns, plain_input);
        JValue *wrong = jnew(J_ARRAY);
        if (actual && jequal(actual, wrong)) fail("turns", "wrong_expectation", "a wrong expected array compared equal");
        jfree(wrong);
        jfree(actual);
    }
    jfree(plain_input);
    expect_empty(persona_core_project_turns, "turns", "wrong_schema", "{\"invalid\":true}");
    expect_empty(persona_core_project_turns, "turns", "invalid_json", "not JSON");
    jfree(overlay);
}

/* ---------- task_index ---------- */

static const char *const task_index_manifest[] = {
    "task_notices_find_calls", "task_title_containing_interrupt_is_not_interrupt",
    "monitor_event_uses_first_detail_line", "task_without_loaded_call_uses_title", "task_status_outcomes",
};

static void check_task_index(const char *fixtures) {
    static const char *const files[] = {
        "folds.json", "frozen.json", "layout.json", "queued.json", "rebuild.json",
        "structure.json", "tasks.json", "wakes.json", "work.json",
    };
    char path[4096];
    size_t replayed = 0;
    int receipt_checked = 0;
    JValue *first_input = NULL; /* cloned: the fixture documents are freed after each file */
    for (size_t f = 0; f < sizeof(files) / sizeof(files[0]); f++) {
        snprintf(path, sizeof(path), "%s/lines/%s", fixtures, files[f]);
        JValue *doc = load_json(path);
        if (!doc) { fail("task_index", files[f], "cannot load fixture"); continue; }
        JValue *cases = jget(doc, "cases");
        for (size_t i = 0; i < jcount(cases); i++) {
            const JValue *c = jat(cases, i);
            const char *name = jstr(jget(c, "name"));
            if (!name) continue;
            const JValue *expected = jget(jget(c, "expected"), "task_index");
            int receipt = strcmp(name, "background_running_badge") == 0;
            if (!expected && !receipt) continue;
            if (jget(jget(c, "input"), "operations")) continue;
            if (!receipt && !manifest_has(task_index_manifest, 5, name)) {
                fail("task_index", name, "case is not in the exact five-case manifest");
                continue;
            }
            if (strcmp(name, "task_notices_find_calls") == 0) {
                jfree(first_input);
                first_input = jclone(jget(c, "input"));
            }
            JValue *actual = project_case(persona_core_project_task_index, jget(c, "input"));
            if (!actual) { fail("task_index", name, "empty projection"); continue; }
            if (receipt) {
                /* A background receipt starts its call with no notices, no statuses and no timestamps, and retains
                 * the original tool description. */
                const JValue *started = jget(actual, "started");
                const char *only = jstr(jat(started, 0));
                if (jcount(started) != 1 || !only || strcmp(only, "call_a") != 0
                    || jcount(jget(actual, "calls")) != 0 || jcount(jget(actual, "statuses")) != 0
                    || jcount(jget(actual, "started_at")) != 0 || jcount(jget(actual, "descriptions")) != 1) {
                    fail("task_index", name, "background receipt boundary differs");
                } else {
                    receipt_checked = 1;
                }
            } else if (!jequal(actual, expected)) {
                fail("task_index", name, "complete expected object differs from the unchanged fixture");
            } else {
                replayed++;
            }
            jfree(actual);
        }
        jfree(doc);
    }
    if (replayed != 5) fail("task_index", "manifest", "exactly five complete expected objects must replay");
    if (!receipt_checked) fail("task_index", "background_running_badge", "background receipt case not replayed");
    printf("c-abi: task_index %zu/5 complete expected objects plus the background receipt\n", replayed);

    if (first_input) {
        JValue *actual = project_case(persona_core_project_task_index, first_input);
        JValue *wrong = jnew(J_OBJECT);
        jset(wrong, "started", jnew(J_ARRAY));
        jset(wrong, "calls", jnew(J_OBJECT));
        jset(wrong, "descriptions", jnew(J_OBJECT));
        jset(wrong, "started_at", jnew(J_OBJECT));
        jset(wrong, "statuses", jnew(J_OBJECT));
        if (actual && jequal(actual, wrong)) fail("task_index", "wrong_expectation", "a wrong expected object compared equal");
        jfree(wrong);
        jfree(actual);
    }
    jfree(first_input);
    expect_empty(persona_core_project_task_index, "task_index", "wrong_schema", "{\"invalid\":true}");
    expect_empty(persona_core_project_task_index, "task_index", "invalid_json", "not JSON");
}

/* ---------- viewed images: real C ABI plus complete ordinary/folded expected objects ---------- */

static JValue *named_case(const JValue *doc, const char *name) {
    JValue *cases = jget(doc, "cases");
    for (size_t i = 0; i < jcount(cases); i++) {
        JValue *c = jat(cases, i);
        const char *candidate = jstr(jget(c, "name"));
        if (candidate && strcmp(candidate, name) == 0) return c;
    }
    return NULL;
}

static JValue *fold_image(const char *item_id, const char *tool_use_id, const char *tool, const char *source,
                          const char *path, long size, long width, long height) {
    JValue *image = jnew(J_OBJECT);
    if (item_id) jset(image, "item_id", jstring(item_id));
    jset(image, "tool_use_id", jstring(tool_use_id));
    jset(image, "tool", jstring(tool));
    jset(image, "source", jstring(source));
    if (path) jset(image, "path", jstring(path));
    jset(image, "mime", jstring("image/png"));
    if (size > 0) jset(image, "size", jnumber(size));
    if (width > 0) jset(image, "width", jnumber(width));
    if (height > 0) jset(image, "height", jnumber(height));
    return image;
}

static JValue *expected_viewed(const char *id, const char *const *ids, size_t count) {
    JValue *expected = jnew(J_OBJECT);
    jset(expected, "id", jstring(id));
    JValue *images = jnew(J_ARRAY);
    for (size_t i = 0; i < count; i++) jpush(images, jstring(ids[i]));
    jset(expected, "image_ids", images);
    jset(expected, "is_input", jbool_value(0));
    jset(expected, "type", jstring("viewed"));
    return expected;
}

static void check_viewed_images(const char *fixtures) {
    char path[4096];
    snprintf(path, sizeof(path), "%s/lines/work.json", fixtures);
    JValue *work = load_json(path);
    JValue *tool = named_case(work, "tool_images_belong_to_call");
    snprintf(path, sizeof(path), "%s/lines/ios/differences.json", fixtures);
    JValue *overlay = load_json(path);
    JValue *entry = named_case(overlay, "tool_images_belong_to_call");
    const JValue *expected = NULL;
    JValue *lines = jget(jget(entry, "replace"), "lines");
    for (size_t i = 0; i < jcount(lines); i++) {
        JValue *line = jat(lines, i);
        const char *type = jstr(jget(line, "type"));
        if (type && strcmp(type, "viewed") == 0) expected = line;
    }
    if (!tool || !expected) fail("viewed_images", "ordinary", "missing source input or complete post-overlay expected");
    JValue *actual = project_case(persona_core_project_viewed_images, jget(tool, "input"));
    if (!actual || !jequal(actual, expected)) {
        fail("viewed_images", "tool_images_belong_to_call", "complete post-overlay viewed object differs");
    }
    if (actual && jequal(actual, jget(jget(tool, "expected"), "lines"))) {
        fail("viewed_images", "overlay_shape", "compared a line with the whole lines array");
    }
    JValue *wrong = jclone(expected);
    jset(wrong, "id", jstring("vi-fold:2#0"));
    if (actual && jequal(actual, wrong)) fail("viewed_images", "wrong_expectation", "wrong owner compared equal");
    jfree(wrong);
    jfree(actual);

    snprintf(path, sizeof(path), "%s/lines/folds.json", fixtures);
    JValue *folds = load_json(path);
    JValue *fold_case = named_case(folds, "fold_counts_tools_and_thoughts");
    JValue *input = jclone(jget(fold_case, "input"));
    JValue *items = jget(input, "items");
    JValue *fold = NULL;
    for (size_t i = 0; i < jcount(items); i++) {
        JValue *item = jat(items, i);
        const char *kind = jstr(jget(item, "kind"));
        if (kind && strcmp(kind, "fold") == 0) fold = jget(item, "fold");
    }
    if (!fold) fail("viewed_images", "fold_input", "source fold not found");
    JValue *images = jnew(J_ARRAY);
    /* ViewedImagesTests.aFoldGivesTheSameStripAsItsWork is the independent source for these four FoldImage values. */
    jpush(images, fold_image("5#0", "a", "Read", "read", "/w/shot.png", 100, 4, 3));
    jpush(images, fold_image("8#0", "b", "mcp__persona__computer_state", "screenshot", NULL, 900, 16, 10));
    jpush(images, fold_image(NULL, "d", "Read", "read", "/w/big.JPG", 0, 0, 0));
    jpush(images, fold_image("20#0", "g", "mcp__persona__browser_look", "browser", NULL, 300, 8, 5));
    jset(fold, "images", images);
    JValue *fold_actual = project_case(persona_core_project_viewed_images, input);
    static const char *const ids[] = {
        "p:/w/shot.png", "s:image/png:900:16x10", "p:/w/big.JPG", "s:image/png:300:8x5",
    };
    JValue *fold_expected = expected_viewed("vi-1#0", ids, 4);
    if (!fold_actual || !jequal(fold_actual, fold_expected)) {
        fail("viewed_images", "folded_work", "complete source-derived folded viewed object differs");
    }
    JValue *wrong_fold = expected_viewed("vi-1#0", ids, 3);
    if (fold_actual && jequal(fold_actual, wrong_fold)) {
        fail("viewed_images", "fold_wrong_expectation", "incomplete expected compared equal");
    }
    jfree(wrong_fold);
    jfree(fold_expected);
    jfree(fold_actual);
    jfree(input);
    jfree(folds);

    expect_empty(persona_core_project_viewed_images, "viewed_images", "wrong_schema", "{\"invalid\":true}");
    expect_empty(persona_core_project_viewed_images, "viewed_images", "invalid_json", "not JSON");
    expect_empty(persona_core_project_viewed_images, "viewed_images", "no_work", "{\"items\":[]}");
    JValue *empty_fold_input = jclone(jget(fold_case, "input"));
    JValue *empty_fold_actual = project_case(persona_core_project_viewed_images, empty_fold_input);
    if (empty_fold_actual) fail("viewed_images", "no_fold_images", "fold without images returned a viewed line");
    jfree(empty_fold_actual);
    jfree(empty_fold_input);
    jfree(overlay);
    jfree(work);
    puts("c-abi: viewed_images ordinary post-overlay and folded complete expected objects; negatives rejected");
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: c_abi_smoke <spec/fixtures directory>\n");
        return 2;
    }
    /* Fail closed: every fixture this client replays must exist and parse before any comparison runs. */
    static const char *const required[] = {
        "sort/filters.json", "stream-chunks/split.json", "answer/questions.json", "answer/recommended.json",
        "lines/ios/differences.json", "lines/folds.json", "lines/frozen.json", "lines/layout.json",
        "lines/queued.json", "lines/rebuild.json", "lines/structure.json", "lines/tasks.json",
        "lines/wakes.json", "lines/work.json",
    };
    for (size_t i = 0; i < sizeof(required) / sizeof(required[0]); i++) {
        char path[4096];
        snprintf(path, sizeof(path), "%s/%s", argv[1], required[i]);
        JValue *doc = load_json(path);
        if (!doc) {
            fprintf(stderr, "c-abi-smoke: FAIL missing or unparseable fixture %s\n", path);
            return 2;
        }
        jfree(doc);
    }
    check_sort(argv[1]);
    check_stream(argv[1]);
    check_answer_file(argv[1], "questions.json", 0, 15);
    check_answer_file(argv[1], "recommended.json", 1, 8);
    check_answer_boundaries();
    check_turns(argv[1]);
    check_task_index(argv[1]);
    check_viewed_images(argv[1]);
    if (failures) {
        fprintf(stderr, "c-abi-smoke: FAIL %d check(s)\n", failures);
        return 1;
    }
    puts("c-abi-smoke: ok independently compiled C client; sort, answer 15+8, stream 21, turns 57 (+overlay), task_index 5+receipt, viewed ordinary+fold; malformed inputs and wrong expectations rejected");
    return 0;
}
