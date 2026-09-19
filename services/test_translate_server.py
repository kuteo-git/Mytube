"""Alignment tests for the batch translation endpoint.

The model is not exercised here. What is exercised is the part that decides
whether the model's answer may be trusted, which is the part that failed in
measurement.
"""
from translate_server import (
    BATCH_PROMPT,
    MERGE_FLOOR,
    MERGE_RATIO,
    OMNIROUTE_API_KEY,
    OMNIROUTE_BASE_URL,
    OMNIROUTE_MODEL,
    aligned_or_none,
    build_body,
    misaligned,
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


# ---- alignment of content, not of count -------------------------------------
#
# The cues and answers below are copied from a real pass over _01Sm2lWwgM, where
# the model merged two cues into line 1 and repeated line 2 as line 3 to keep the
# count. Every check that existed at the time passed it.

REAL_CUES = [
    "So, you may or may not know this about me,",
    "but Ive been a huge Pokemon fanatic since the very beginning.",
    "I remember back in 1999 walking into a Best Buy with my dad "
    "and picking up my very first version of Pokemon Blue.",
]

REAL_ANSWER = [
    "Bạn có thể biết hoặc không biết về tôi, nhưng tôi là một fan cuồng "
    "Pokemon từ rất lâu rồi.",
    "Tôi còn nhớ hồi năm 1999, tôi đi vào cửa hàng Best Buy cùng bố và lấy "
    "phiên bản Pokemon Blue đầu tiên của mình.",
    "Tôi còn nhớ hồi năm 1999, tôi đi vào cửa hàng Best Buy cùng bố và lấy "
    "phiên bản Pokemon Blue đầu tiên của mình.",
]


def test_misaligned_catches_the_answer_that_shipped():
    assert misaligned(REAL_CUES, REAL_ANSWER)


def test_misaligned_passes_an_ordinary_batch():
    cues = ["Hello there.", "How are you?", "Fine, thanks."]
    out = ["Chào bạn.", "Bạn khỏe không?", "Mình ổn, cảm ơn."]
    assert misaligned(cues, out) == ""


def test_a_repeat_is_allowed_when_the_cues_repeat_too():
    # The model is not wrong to answer the same line twice for the same words.
    cues = ["Yeah.", "Something else.", "Yeah."]
    out = ["Ừ.", "Chuyện khác.", "Ừ."]
    assert misaligned(cues, out) == ""


def test_a_blank_line_is_not_a_repeat():
    # An untranslated line is absence, and two absences are not a shift.
    cues = ["One.", "Two.", "Three."]
    out = ["Một.", "", ""]
    assert misaligned(cues, out) == ""


def test_a_short_line_may_translate_long():
    # "Yeah." to a full Vietnamese phrase is a ratio of three and is not a merge.
    assert misaligned(["Yeah."], ["Ừ, đúng rồi đấy bạn ạ."]) == ""


# ---- alignment of content: the numbers a line carries ------------------------
#
# The two signatures above both look for the *padding* a shift leaves behind — a
# repeated line, or two cues merged into one over-long line. A model that hands
# back the right number of lines, each the right length, attached to the wrong
# cues leaves no padding at all, and both go blind.
#
# The cues and answers below are copied from a real pass over kXVt4atqMv8,
# narrated on 18 September by the server that already had both signatures. Six
# consecutive cues carried the next cue's words and nothing refused the batch:
# every output line was distinct, and the longest was 54 characters against a
# merge threshold of 122.

ROTATED_CUES = [
    "And of course, it's also worth remembering that with the AirPods 4,",
    "we also got USBC.",
    "So, if you're coming from the three or the twos,",
    "you also get that as an upgrade as well.",
    "And finally, what about any special features that you're getting with the AirPods 5?",
    "Well, if you're upgrading from the AirPods 4,",
    "you now get even better water resistance.",
    "Going from IP54 to IP57,",
]

ROTATED_ANSWER = [
    "Và tất nhiên, cần nhớ rằng với AirPods 4,",
    "Vậy nếu bạn dùng thế hệ 3 hay 2,",
    "bạn cũng được nâng cấp đó.",
    "Cuối cùng, AirPods 5 có những tính năng đặc biệt gì?",
    "Nếu bạn nâng cấp từ AirPods 4,",
    "bạn sẽ có khả năng chống nước tốt hơn.",
    "Chúng tôi cũng nhận được điều đó như một bản nâng cấp.",
    "Từ IP54 lên IP57,",
]


def test_misaligned_catches_a_rotation_with_no_padding():
    assert misaligned(ROTATED_CUES, ROTATED_ANSWER)


def test_the_two_older_signatures_are_blind_to_it():
    # Recorded rather than assumed: this is why a third signature exists. If a
    # later change makes either of these fire, the third is no longer the only
    # thing standing between this batch and the cache.
    seen = {}
    repeated = False
    for i, line in enumerate(ROTATED_ANSWER):
        key = line.strip().casefold()
        first = seen.get(key)
        if first is not None and ROTATED_CUES[first].strip() != ROTATED_CUES[i].strip():
            repeated = True
        seen.setdefault(key, i)
    assert not repeated

    merged = any(
        len(line.strip()) > MERGE_FLOOR + MERGE_RATIO * len(ROTATED_CUES[i].strip())
        for i, line in enumerate(ROTATED_ANSWER)
    )
    assert not merged


def test_a_spelled_out_number_may_be_written_as_a_digit():
    # The whole reason this cannot be "a digit the source does not have": every
    # one of these is a correct translation. "the twos" is 2, "a 6 out of 10" is
    # already digits, and Vietnamese writes them all as digits.
    cues = [
        "So, if you're coming from the three or the twos,",
        "The AirPods 3, however, were about a 6 out of 10.",
        "Let's say they've got a ten out of ten sound quality.",
    ]
    out = [
        "Vậy nếu bạn dùng thế hệ 3 hay 2,",
        "Còn AirPods 3 thì chỉ được khoảng 6 điểm.",
        "Cứ cho là chất lượng âm thanh của chúng là 10 trên 10.",
    ]
    assert misaligned(cues, out) == ""


def test_a_number_the_translator_adds_is_not_a_shift():
    # Nothing nearby carries it either, so there is nothing to have been shifted
    # from. A translator elaborating is not a translator misaligned.
    cues = ["It costs a fortune.", "But it is worth it."]
    out = ["Nó tốn cả gia tài, gấp 3 lần.", "Nhưng đáng đồng tiền."]
    assert misaligned(cues, out) == ""


def test_the_last_line_has_no_next_cue_to_borrow_from():
    cues = ["Here we go.", "It cost 500 dollars."]
    out = ["Bắt đầu nào.", "Nó giá 500 đô."]
    assert misaligned(cues, out) == ""


def test_a_month_name_is_the_number_of_its_month():
    # "starting in July this year" is correctly "bắt đầu từ tháng 7 năm nay",
    # and the cue after it happened to mention 7 as well. Measured across the
    # library, month names were the single largest source of false alarms.
    cues = ["but starting in July this year,", "the average increase has reached 7 to 10%."]
    out = ["nhưng bắt đầu từ tháng 7 năm nay,", "mức tăng trung bình đã lên tới 7 đến 10%."]
    assert misaligned(cues, out) == ""


def test_a_number_written_a_different_way_is_not_borrowed():
    # "how old were you on 911?" is correctly "vào ngày 11/9?" — the digits in
    # the answer are the digits in the question, punctuated for a reader who
    # writes the day first. The next cue saying "nine" is a coincidence.
    cues = ["John, how old were you on 911?",
            "I was a week away from being 8 years old and I was nine."]
    out = ["John, bạn bao nhiêu tuổi vào ngày 11/9?",
           "Tôi còn một tuần nữa sẽ tròn 8 tuổi, hay là tôi đã 9 tuổi."]
    assert misaligned(cues, out) == ""


def test_a_borrowed_number_is_still_caught_when_nothing_explains_it():
    # The shape that must survive both excuses above: the line names a thing
    # from the cue after it and its own cue has no number in any form.
    cues = ["It actually found a security loophole and called it out.",
            "This is a P1 loophole."]
    out = ["Đây là một lỗ hổng P1.",
           "Nó cho bạn một prompt để dán vào agent."]
    assert misaligned(cues, out)
