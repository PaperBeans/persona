//! Replays the exact current-iOS question-answer and recommendation-display fixture contracts.

use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

use persona_core::answer::Input;
use serde_json::{Map, Value, json};

fn fixture(file: &str) -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../../spec/fixtures/answer")
        .join(file)
}

#[test]
fn question_and_recommended_cases_match_complete_expected_objects() {
    for (file, expected_names) in [
        (
            "questions.json",
            [
                "all_answered_can_submit_from_earlier_page",
                "blank_other_cannot_submit",
                "blank_other_uses_picks",
                "empty_answer_cannot_submit",
                "last_page_missing_answer_returns_to_gap",
                "multiple_other_overrides_picks",
                "multiple_pick_toggles_off",
                "multiple_pick_waits_for_submit",
                "multiple_picks_follow_option_order",
                "multiple_questions_send_original_keys",
                "next_page_does_not_send",
                "other_answer_trimmed",
                "sending_disables_option",
                "single_pick_clears_other",
                "single_tap_sends_original_label",
            ]
            .as_slice(),
        ),
        (
            "recommended.json",
            [
                "recommended_case_sensitive",
                "recommended_chinese_ascii",
                "recommended_chinese_fullwidth",
                "recommended_suffix_only",
                "recommended_spaced",
                "recommended_trailing_space",
                "recommended_trims_prefix_space",
                "recommended_unspaced",
            ]
            .as_slice(),
        ),
    ] {
        let document: Value = serde_json::from_slice(&fs::read(fixture(file)).unwrap()).unwrap();
        assert_eq!(document["schema_version"], 1);
        let mut replayed = BTreeSet::new();
        for case in document["cases"].as_array().unwrap() {
            let name = case["name"].as_str().unwrap();
            assert!(case["derived"] == true && case.get("from_test").is_none());
            assert_eq!(case["input"]["profile"], "ios_current", "{file}/{name}");
            let input: Input = serde_json::from_value(case["input"].clone()).expect(name);
            let transition = persona_core::answer::project(&input).expect(name);
            let mut actual = Map::new();
            actual.insert("allowed".to_owned(), json!(transition.allowed));
            actual.insert(
                "resolution".to_owned(),
                transition
                    .resolution
                    .as_ref()
                    .map(|resolution| serde_json::to_value(resolution).unwrap())
                    .unwrap_or(Value::Null),
            );
            if transition.page != input.operation.page {
                actual.insert("next_page".to_owned(), json!(transition.page));
            }
            if input.operation.action == persona_core::answer::Action::PickOption
                && transition.allowed
                && input.interaction.questions[input.operation.page].multi_select
            {
                actual.insert("picks".to_owned(), serde_json::to_value(&transition.picks).unwrap());
            }
            if file == "recommended.json" {
                let label = &input.interaction.questions[input.operation.page].options
                    [input.operation.option_index.unwrap()]
                .label;
                actual.insert(
                    "display".to_owned(),
                    serde_json::to_value(persona_core::answer::display(label)).unwrap(),
                );
            }
            assert_eq!(Value::Object(actual), case["expected"], "{file}/{name}");
            assert!(replayed.insert(name.to_owned()), "duplicate case: {name}");
        }
        assert_eq!(
            replayed,
            expected_names.iter().map(|name| (*name).to_owned()).collect(),
            "exact manifest for {file}"
        );
        eprintln!("answer projection: {file}: {} full expected objects", replayed.len());
    }
}

#[test]
fn selected_labels_follow_option_order_not_pick_order_or_display_text() {
    let mut input: Input = serde_json::from_value(json!({
        "interaction":{"kind":"question","questions":[{
            "question":"Pick two","multi_select":true,
            "options":[{"label":"z (Recommended)"},{"label":"a"},{"label":"m"}],
        }]},
        "operation":{
            "action":"pick_option","page":0,"option_index":2,
            "picks":{"Pick two":["z (Recommended)"]},"other":{},"sending":false,
        }
    }))
    .unwrap();
    let picked = persona_core::answer::project(&input).unwrap();
    assert_eq!(picked.picks["Pick two"].len(), 2);
    input.operation.picks = picked.picks;
    input.operation.other = picked.other;
    input.operation.action = persona_core::answer::Action::Submit;
    let transition = persona_core::answer::project(&input).unwrap();
    assert_eq!(transition.resolution.unwrap().answers["Pick two"], "z (Recommended), m");
}

#[test]
fn disallowed_operations_leave_the_form_unchanged() {
    for action in ["advance", "submit"] {
        let input: Input = serde_json::from_value(json!({
            "interaction":{"kind":"question","questions":[{
                "question":"One","multi_select":false,"options":[{"label":"Yes"}],
            }]},
            "operation":{"action":action,"page":0,"picks":{"One":["Yes"]},"other":{},"sending":true},
        }))
        .unwrap();
        let transition = persona_core::answer::project(&input).unwrap();
        assert!(!transition.allowed);
        assert_eq!(transition.page, input.operation.page);
        assert_eq!(transition.picks, input.operation.picks);
        assert_eq!(transition.other, input.operation.other);
        assert_eq!(transition.resolution, None);
    }
}

#[test]
fn apple_whitespace_sets_include_zero_width_space_but_display_preserves_newlines() {
    // CoreFoundation's isWhitespace covers U+2000...U+200B; its newline set is separate.
    let input: Input = serde_json::from_value(json!({
        "interaction":{"kind":"question","questions":[{
            "question":"One","multi_select":false,"options":[{"label":"Yes"}],
        }]},
        "operation":{"action":"submit","page":0,"picks":{},"other":{"One":" \u{200b} "},"sending":false},
    }))
    .unwrap();
    let transition = persona_core::answer::project(&input).unwrap();
    assert!(!transition.allowed);
    assert_eq!(transition.resolution, None);

    let shown = persona_core::answer::display("\u{200b} A\n (Recommended)");
    assert_eq!(shown.text, "A\n");
    assert!(shown.recommended);
}
