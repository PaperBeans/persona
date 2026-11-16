// An independently compiled C consumer of the checked-in C header (#8560).
// The Rust tests exercise the same exports from Rust; this client proves the C declarations, link and buffer contract.
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "persona_core.h"

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

static int check_projection(const char *input_path, const char *expected_path) {
    size_t input_length = 0, expected_length = 0;
    void *input = read_file(input_path, &input_length);
    void *expected = read_file(expected_path, &expected_length);
    if (!input || !expected) {
        free(input);
        free(expected);
        return 1;
    }
    PersonaCoreBytes result = persona_core_project_sort(input, input_length);
    int failed = !result.data || result.length != expected_length
        || memcmp(result.data, expected, expected_length) != 0;
    if (failed) {
        fprintf(stderr, "the independently linked projection differs from the fixture: length=%zu expected=%zu\n",
                result.length, expected_length);
    }
    persona_core_bytes_free(result);
    free(input);
    free(expected);
    return failed;
}

static int check_empty_result(const unsigned char *input, size_t length, const char *label) {
    PersonaCoreBytes result = persona_core_project_sort(input, length);
    int failed = result.data || result.length != 0 || result.capacity != 0;
    if (failed) {
        fprintf(stderr, "%s did not return an empty buffer\n", label);
    }
    // Release the buffer this call actually returned, including an unexpected non-empty failure result.
    persona_core_bytes_free(result);
    return failed;
}

static int check_malformed(void) {
    const unsigned char wrong_schema[] = "{\"invalid\":true}";
    if (check_empty_result(wrong_schema, sizeof(wrong_schema) - 1, "valid JSON with the wrong schema")) {
        return 1;
    }
    const unsigned char invalid_json[] = "{\"sessions\":[}";
    if (check_empty_result(invalid_json, sizeof(invalid_json) - 1, "syntactically invalid JSON")) {
        return 1;
    }
    const unsigned char empty[] = "";
    return check_empty_result(empty, 0, "empty input");
}

int main(int argc, char **argv) {
    if (argc != 3) {
        fprintf(stderr, "usage: c_abi_smoke <empty-list.json> <expected.json>\n");
        return 2;
    }
    if (check_projection(argv[1], argv[2]) || check_malformed()) {
        return 1;
    }
    puts("c-abi-smoke: ok independently compiled C client, projection and empty-buffer release");
    return 0;
}
