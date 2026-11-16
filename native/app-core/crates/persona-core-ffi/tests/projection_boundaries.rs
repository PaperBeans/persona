//! Replays the answer, stream-chunk, turn, task-index and viewed-image corpora through the exported C ABI, including
//! each returned buffer. The independently compiled C11 client (`tests/c_abi_smoke.c`) replays the same corpus from
//! outside Rust; these tests pin the same contract for a Rust caller of the ABI, not for Swift or a device.

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};
use std::slice;

use persona_core::{
    PersonaCoreBytes, persona_core_bytes_free, persona_core_display_answer, persona_core_project_answer,
    persona_core_project_stream_chunks, persona_core_project_task_index, persona_core_project_turns,
    persona_core_project_viewed_images,
};
use serde_json::Value;

fn fixtures() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../../spec/fixtures")
}

fn call(projection: unsafe extern "C" fn(*const u8, usize) -> PersonaCoreBytes, input: &[u8]) -> Option<Vec<u8>> {
    let bytes = unsafe { projection(input.as_ptr(), input.len()) };
    if bytes.data.is_null() {
        assert_eq!((bytes.length, bytes.capacity), (0, 0));
        return None;
    }
    let output = unsafe { slice::from_raw_parts(bytes.data, bytes.length) }.to_vec();
    unsafe { persona_core_bytes_free(bytes) };
    Some(output)
}

fn project(projection: unsafe extern "C" fn(*const u8, usize) -> PersonaCoreBytes, input: &Value) -> Value {
    let input = serde_json::to_vec(input).unwrap();
    let output = call(projection, &input).expect("the C ABI returned no projection");
    serde_json::from_slice(&output).expect("the C ABI returned non-JSON bytes")
}

#[test]
fn c_abi_answer_replays_questions_and_recommendations_and_rejects_bad_input() {
    let mut replayed = BTreeSet::new();
    for (file, recommended, count) in [("questions.json", false, 15), ("recommended.json", true, 8)] {
        let document: Value = serde_json::from_slice(&fs::read(fixtures().join("answer").join(file)).unwrap()).unwrap();
        let cases = document["cases"].as_array().unwrap();
        assert_eq!(cases.len(), count, "{file}: exact case count");
        for case in cases {
            let name = case["name"].as_str().unwrap().to_owned();
            let input = &case["input"];
            let transition = project(persona_core_project_answer, input);
            let operation = &input["operation"];
            let mut actual = serde_json::json!({
                "allowed": transition["allowed"],
                "resolution": transition["resolution"],
            });
            if transition["page"] != operation["page"] {
                actual["next_page"] = transition["page"].clone();
            }
            let question = &input["interaction"]["questions"][operation["page"].as_u64().unwrap() as usize];
            if operation["action"] == "pick_option"
                && transition["allowed"] == Value::Bool(true)
                && question["multi_select"] == Value::Bool(true)
            {
                actual["picks"] = transition["picks"].clone();
            }
            if recommended {
                let label = question["options"][operation["option_index"].as_u64().unwrap() as usize]["label"]
                    .as_str()
                    .unwrap();
                let display = project(persona_core_display_answer, &Value::String(label.to_owned()));
                actual["display"] = display;
            }
            assert_eq!(actual, case["expected"], "C ABI answer: {name}");
            assert!(replayed.insert(name), "duplicate answer case");
        }
    }
    assert_eq!(replayed.len(), 23, "15 question + 8 recommendation cases");
    assert!(call(persona_core_project_answer, b"{\"invalid\":true}").is_none());
    assert!(call(persona_core_project_answer, b"not JSON").is_none());
    assert!(call(persona_core_project_answer, &[0xff]).is_none());
    assert!(call(persona_core_display_answer, b"{\"label\":1}").is_none());
    eprintln!("C ABI answer replay: {} complete expected objects", replayed.len());
}

#[test]
fn c_abi_stream_chunks_replays_all_cases_and_rejects_bad_input() {
    let document: Value =
        serde_json::from_slice(&fs::read(fixtures().join("stream-chunks/split.json")).unwrap()).unwrap();
    let cases = document["cases"].as_array().unwrap();
    assert_eq!(cases.len(), 21);
    let mut replayed = BTreeSet::new();
    for case in cases {
        let name = case["name"].as_str().unwrap().to_owned();
        let actual = project(persona_core_project_stream_chunks, &case["input"]);
        assert_eq!(actual, case["expected"], "C ABI stream_chunks: {name}");
        assert!(replayed.insert(name), "duplicate stream case");
    }
    assert_eq!(replayed.len(), 21);
    assert!(call(persona_core_project_stream_chunks, b"{\"invalid\":true}").is_none());
    assert!(call(persona_core_project_stream_chunks, b"not JSON").is_none());
    eprintln!(
        "C ABI stream_chunks replay: {} complete {{done, tail}} objects",
        replayed.len()
    );
}

fn lines_documents() -> Vec<Value> {
    let mut paths: Vec<PathBuf> = fs::read_dir(fixtures().join("lines"))
        .expect("lines fixtures")
        .map(|entry| entry.expect("directory entry").path())
        .filter(|path| path.extension().is_some_and(|extension| extension == "json"))
        .collect();
    paths.sort();
    assert_eq!(paths.len(), 9, "the lines file set is the manifest boundary");
    paths
        .iter()
        .map(|path| serde_json::from_slice(&fs::read(path).unwrap()).unwrap())
        .collect()
}

/// The current-iOS overlay, applied by the caller exactly as the Kotlin and C-client replays apply it.
fn expected_turns(case: &Value, overlay: &Value) -> Value {
    let name = case["name"].as_str().unwrap();
    let wake = &overlay["turn_wake_state"];
    let drop = wake["drop"].as_str().unwrap();
    let set = wake["set"].as_str().unwrap();
    let mut expected = case["expected"].clone();
    let turns = expected["turns"].as_array_mut().unwrap();
    for turn in turns.iter_mut() {
        let turn = turn.as_object_mut().unwrap();
        assert!(turn.remove(drop).is_some(), "{name}: stale wake drop");
        let id = turn["id"].as_str().unwrap();
        let pending = wake["pending"].as_array().unwrap().iter().any(|reference| {
            reference["case"] == Value::String(name.to_owned()) && reference["turn"] == Value::String(id.to_owned())
        });
        turn.insert(set.to_owned(), Value::Bool(pending));
    }
    for entry in overlay["cases"].as_array().unwrap() {
        if entry["name"] != Value::String(name.to_owned()) {
            continue;
        }
        if let Some(replace) = entry.get("replace") {
            for (key, value) in replace.as_object().unwrap() {
                expected[key] = value.clone();
            }
        }
        if let Some(fields) = entry.get("turn_fields") {
            for (id, patch) in fields.as_object().unwrap() {
                let mut matched = 0;
                for turn in expected["turns"].as_array_mut().unwrap() {
                    if turn["id"] == Value::String(id.clone()) {
                        matched += 1;
                        for (key, value) in patch.as_object().unwrap() {
                            turn[key] = value.clone();
                        }
                    }
                }
                assert_eq!(matched, 1, "{name}/{id}: the overlay must name exactly one turn");
            }
        }
    }
    expected["turns"].clone()
}

#[test]
fn c_abi_turns_replays_the_static_corpus_with_the_overlay() {
    let overlay: Value =
        serde_json::from_slice(&fs::read(fixtures().join("lines/ios/differences.json")).unwrap()).unwrap();
    let mut replayed = BTreeSet::new();
    let mut turn_objects = 0;
    for document in lines_documents() {
        for case in document["cases"].as_array().unwrap() {
            let name = case["name"].as_str().unwrap().to_owned();
            if case["expected"].get("checkpoints").is_some() || case["input"].get("operations").is_some() {
                continue;
            }
            let actual = project(persona_core_project_turns, &case["input"]);
            let expected = expected_turns(case, &overlay);
            assert_eq!(actual, expected, "C ABI turns: {name}");
            turn_objects += expected.as_array().unwrap().len();
            assert!(replayed.insert(name), "duplicate static turn case");
        }
    }
    assert_eq!(replayed.len(), 57, "the exact static turn manifest");
    assert_eq!(turn_objects, 62, "the 54 non-empty cases hold 62 complete turn objects");
    assert!(call(persona_core_project_turns, b"{\"invalid\":true}").is_none());
    assert!(call(persona_core_project_turns, b"not JSON").is_none());
    eprintln!(
        "C ABI turns replay: {} static cases, {turn_objects} turn objects",
        replayed.len()
    );
}

#[test]
fn c_abi_task_index_replays_all_cases_and_rejects_bad_input() {
    let mut replayed = BTreeSet::new();
    let mut receipt = false;
    for document in lines_documents() {
        for case in document["cases"].as_array().unwrap() {
            let name = case["name"].as_str().unwrap().to_owned();
            if case["input"].get("operations").is_some() {
                continue;
            }
            let expected = &case["expected"]["task_index"];
            let is_receipt = name == "background_running_badge";
            if expected.is_null() && !is_receipt {
                continue;
            }
            let actual = project(persona_core_project_task_index, &case["input"]);
            if is_receipt {
                assert_eq!(actual["started"], serde_json::json!(["call_a"]));
                assert_eq!(actual["calls"], serde_json::json!({}));
                assert_eq!(actual["statuses"], serde_json::json!({}));
                assert_eq!(actual["started_at"], serde_json::json!({}));
                receipt = true;
                continue;
            }
            assert_eq!(actual, *expected, "C ABI task_index: {name}");
            assert!(replayed.insert(name), "duplicate task-index case");
        }
    }
    assert_eq!(replayed.len(), 5, "the exact five-case task-index manifest");
    assert!(receipt, "the background receipt boundary must be replayed");
    assert!(call(persona_core_project_task_index, b"{\"invalid\":true}").is_none());
    assert!(call(persona_core_project_task_index, b"not JSON").is_none());
    eprintln!(
        "C ABI task_index replay: {} complete expected objects plus the receipt",
        replayed.len()
    );
}

#[test]
fn c_abi_viewed_images_preserves_the_complete_overlay_line_and_rejects_bad_input() {
    let overlay: Value =
        serde_json::from_slice(&fs::read(fixtures().join("lines/ios/differences.json")).unwrap()).unwrap();
    let case = lines_documents()
        .into_iter()
        .flat_map(|document| document["cases"].as_array().unwrap().clone())
        .find(|case| case["name"] == "tool_images_belong_to_call")
        .expect("the ordinary viewed-image case must exist");
    let expected = overlay["cases"]
        .as_array()
        .unwrap()
        .iter()
        .find(|entry| entry["name"] == case["name"])
        .and_then(|entry| entry["replace"]["lines"].as_array())
        .unwrap()
        .iter()
        .find(|line| line["type"] == "viewed")
        .unwrap();
    let actual = project(persona_core_project_viewed_images, &case["input"]);
    assert_eq!(actual, *expected);
    assert_ne!(
        actual,
        serde_json::json!({
            "id": "vi-wrong",
            "image_ids": expected["image_ids"],
            "is_input": false,
            "type": "viewed",
        })
    );
    assert!(call(persona_core_project_viewed_images, b"{\"invalid\":true}").is_none());
    assert!(call(persona_core_project_viewed_images, b"not JSON").is_none());
    assert!(call(persona_core_project_viewed_images, b"{\"items\":[]}").is_none());
}
