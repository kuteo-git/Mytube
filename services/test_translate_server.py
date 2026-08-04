"""Alignment tests for the batch translation endpoint.

The model is not exercised here. What is exercised is the part that decides
whether the model's answer may be trusted, which is the part that failed in
measurement.
"""
from translate_server import (
    BATCH_PROMPT,
    OMNIROUTE_API_KEY,
    OMNIROUTE_BASE_URL,
    OMNIROUTE_MODEL,
    aligned_or_none,
    build_body,
    openai_content,
    parse_numbered,
    resolve_config,
    strip_budget,
)


def test_parse_numbered_reads_dot_and_paren():
    out = parse_numbered("1. một\n2) hai\n3. ba")
    assert out == {1: "một", 2: "hai", 3: "ba"}


def test_parse_numbered_ignores_preamble():
    # Gemma was measured emitting a lead-in outside the requested format.
    out = parse_numbered("Bản dịch:\n1. một\n2. hai")
    assert out == {1: "một", 2: "hai"}


def test_parse_numbered_strips_think_block():
    out = parse_numbered("<think>hmm</think>\n1. một")
    assert out == {1: "một"}


def test_aligned_returns_list_when_every_line_present():
    assert aligned_or_none({1: "a", 2: "b"}, 2) == ["a", "b"]


def test_aligned_returns_none_on_short_answer():
    # The measured failure: 15 sent, fewer returned, rest silently shifted.
    assert aligned_or_none({1: "a", 2: "b"}, 3) is None


def test_aligned_returns_none_on_gap():
    assert aligned_or_none({1: "a", 3: "c"}, 3) is None


def test_openai_content_reads_a_plain_response():
    payload = {"choices": [{"message": {"content": "1. một\n2. hai"}}]}
    assert openai_content(payload) == "1. một\n2. hai"


def test_openai_content_ignores_reasoning():
    # sub_translation routes to a reasoning model, which returns its working in
    # a separate field. Only the answer is the translation.
    payload = {
        "choices": [
            {"message": {"content": "1. một", "reasoning_content": "hmm 2. hai"}}
        ]
    }
    assert openai_content(payload) == "1. một"


def test_openai_content_survives_a_shape_it_does_not_know():
    assert openai_content({"error": "nope"}) == ""
    assert openai_content({"choices": []}) == ""


def test_config_prefers_the_request_over_the_environment():
    # The gateway owns the configuration and sends it down with every batch, so
    # a saved change takes effect on the next batch rather than on a restart.
    got = resolve_config({
        "baseUrl": "http://req:1",
        "model": "req_model",
        "apiKey": "sk-req",
    })
    assert got == ("http://req:1", "req_model", "sk-req")


def test_config_falls_back_to_the_environment():
    # Nothing sent: whatever the process was started with.
    got = resolve_config({})
    assert got == (OMNIROUTE_BASE_URL, OMNIROUTE_MODEL, OMNIROUTE_API_KEY)


def test_config_ignores_blanks_rather_than_taking_them():
    # An empty field is a field the caller did not fill in, not a request to
    # translate against an empty base url.
    got = resolve_config({"baseUrl": "", "model": "", "apiKey": ""})
    assert got == (OMNIROUTE_BASE_URL, OMNIROUTE_MODEL, OMNIROUTE_API_KEY)


def test_batch_request_turns_thinking_off(monkeypatch):
    """The router bills and delays for reasoning we then discard.

    Measured against the real router on a 15-cue batch: with this field the
    call went from 5.1s and 649 completion tokens to 2.5s and 345, and the
    translation was unchanged. The two switches usually reached for first —
    chat_template_kwargs.enable_thinking and a "/no_think" suffix — were both
    accepted and ignored, so this one is load-bearing rather than belt-and-braces.
    """
    import json
    import translate_server

    sent = {}

    class FakeResponse:
        def __enter__(self):
            return self

        def __exit__(self, *_):
            return False

        def read(self):
            return json.dumps(
                {"choices": [{"message": {"content": "1. một"}}]}
            ).encode()

    def fake_urlopen(req, timeout=None):
        sent["payload"] = json.loads(req.data)
        return FakeResponse()

    monkeypatch.setattr(translate_server.urllib.request, "urlopen", fake_urlopen)
    monkeypatch.setattr(translate_server.json, "load", lambda r: json.loads(r.read()))

    translate_server.omniroute_batch(
        ["hello"], [], base_url="http://router", model="m", api_key="k"
    )
    assert sent["payload"]["thinking"] == {"type": "disabled"}
    # Streaming stays off for the same reason it always was: a body of SSE
    # frames is not JSON.
    assert sent["payload"]["stream"] is False


# ---- the per-line time budget -----------------------------------------------
#
# These lines are read aloud over a video, so each has only until the next
# subtitle. A translation that runs long is not trimmed, it is sped up — and
# past about 3x the voice stops being followable, at which point the line is
# dropped. Handing the model the budget moves that from a playback problem to a
# wording one.


def test_build_body_prefixes_each_line_with_its_budget():
    body = build_body(["hello", "world"], [2.4, 1.0])
    assert body == "1. [2.4s] hello\n2. [1.0s] world"


def test_build_body_without_slots_is_the_plain_list():
    # The single-cue retry path knows the text but not always the timing.
    assert build_body(["hello", "world"], None) == "1. hello\n2. world"


def test_build_body_omits_the_budget_when_there_is_none():
    # The last cue of a video has no following cue, so no slot. An empty "[]"
    # would read as a budget of nothing and invite a one-word translation.
    body = build_body(["a", "b", "c"], [2.0, 0, None])
    assert body == "1. [2.0s] a\n2. b\n3. c"


def test_build_body_tolerates_a_short_slot_list():
    assert build_body(["a", "b"], [2.0]) == "1. [2.0s] a\n2. b"


def test_prompt_states_the_budget_rule_and_the_idiom_rule():
    # Both were asked for explicitly. A prompt that quietly loses one of them
    # fails in a way no alignment check can see.
    assert "[2.4s]" in BATCH_PROMPT
    assert "NEVER drop information" in BATCH_PROMPT
    assert "idioms" in BATCH_PROMPT.lower()


# ---- the budget must never reach the speakers -------------------------------


def test_strip_budget_removes_a_copied_prefix():
    # Left in place, parse_numbered folds it into the translation and the
    # narrator reads it out: the viewer hears "hai phẩy bốn giây" mid-film.
    assert strip_budget("[2.4s] xin chào") == "xin chào"


def test_strip_budget_tolerates_spacing_and_comma_decimals():
    for line in ("[2.4s] xin chào", "[ 2.4 s ] xin chào",
                 "[2,4s] xin chào", "[2S] xin chào", "[10s]  xin chào"):
        assert strip_budget(line) == "xin chào", line


def test_strip_budget_leaves_real_bracketed_content_alone():
    # Subtitles genuinely open with brackets — speaker labels, sound cues.
    assert strip_budget("[nhạc nền] xin chào") == "[nhạc nền] xin chào"
    assert strip_budget("[2.4] xin chào") == "[2.4] xin chào"
    assert strip_budget("[s] xin chào") == "[s] xin chào"


def test_strip_budget_removes_only_the_leading_one():
    # A duration inside the sentence is content, not a prefix.
    assert strip_budget("[2.4s] chờ [1.0s] nhé") == "chờ [1.0s] nhé"


def test_parse_numbered_strips_the_budget_from_every_line():
    out = parse_numbered("1. [2.4s] một\n2. [1.0s] hai")
    assert out == {1: "một", 2: "hai"}
