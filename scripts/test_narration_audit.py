"""Tests for the narration audit.

What is exercised is the part that decides whether a video's stored narration
is shifted, and the part that recovers the order it was spoken in. Neither
needs a model, a server or the library itself.
"""
import importlib.util
import json
import os
import sys

_here = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(_here, "..", "services"))
_spec = importlib.util.spec_from_file_location(
    "narration_audit", os.path.join(_here, "narration-audit.py"))
audit = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(audit)


# The batch below is copied from a real pass over kXVt4atqMv8, narrated by the
# server that already had the repeat signature. Six consecutive cues carried the
# next cue's words and nothing refused it.
ROTATED_CUES = [
    "And of course, it's also worth remembering that with the AirPods 4,",
    "we also got USBC.",
    "So, if you're coming from the three or the twos,",
    "you also get that as an upgrade as well.",
    "Going from IP54 to IP57,",
]

ROTATED_ANSWER = [
    "Và tất nhiên, cần nhớ rằng với AirPods 4,",
    "Vậy nếu bạn dùng thế hệ 3 hay 2,",
    "bạn cũng được nâng cấp đó.",
    "Cuối cùng, AirPods 5 có những tính năng đặc biệt gì?",
    "Từ IP54 lên IP57,",
]


def test_borrowed_catches_the_rotation_the_repeat_signature_misses():
    assert audit.suspect(ROTATED_CUES, ROTATED_ANSWER) == ""
    assert audit.borrowed(ROTATED_CUES, ROTATED_ANSWER)


def test_borrowed_allows_a_spelled_out_number_written_as_a_digit():
    # Every one of these is a correct translation, and Vietnamese writes a
    # spelled-out English number as a digit as a matter of course.
    cues = [
        "So, if you're coming from the three or the twos,",
        "The AirPods 3, however, were about a 6 out of 10.",
        "Let's say they've got a ten out of ten sound quality.",
    ]
    out = [
        "Vậy nếu bạn dùng thế hệ 3 hay 2,",
        "Còn AirPods 3 thì chỉ được khoảng 6 điểm.",
        "Cứ cho là chất lượng âm thanh là 10 trên 10.",
    ]
    assert audit.borrowed(cues, out) == ""


def test_borrowed_ignores_a_number_nothing_nearby_carries():
    # A translator elaborating is not a translator misaligned.
    cues = ["It costs a fortune.", "But it is worth it."]
    out = ["Nó tốn cả gia tài, gấp 3 lần.", "Nhưng đáng đồng tiền."]
    assert audit.borrowed(cues, out) == ""


def test_borrowed_tolerates_a_short_translation_list():
    assert audit.borrowed(["one", "two", "three"], ["một"]) == ""


# ---- recovering the order without narration-cues.json -----------------------

ROLLING_VTT = """WEBVTT
Kind: captions
Language: en

00:00:00.000 --> 00:00:02.000
 
The brand new AirPods 5 are here.

00:00:02.000 --> 00:00:04.000
The brand new AirPods 5 are here.
Apple gave me an early look.

00:00:04.000 --> 00:00:06.000
Apple gave me an early look.
So, should you upgrade?
"""


def test_order_is_recovered_from_the_captions():
    # Deliberately out of order in the dict, which is what a JSON cache is.
    cache = {
        "So, should you upgrade?": "Vậy có nên nâng cấp không?",
        "The brand new AirPods 5 are here.": "AirPods 5 mới đã ra mắt.",
        "Apple gave me an early look.": "Apple cho mình xem trước.",
    }
    assert audit.order_from_transcript(cache, ROLLING_VTT) == [
        "The brand new AirPods 5 are here.",
        "Apple gave me an early look.",
        "So, should you upgrade?",
    ]


def test_a_rolling_repeat_does_not_place_a_cue_twice():
    cache = {"Apple gave me an early look.": "Apple cho mình xem trước."}
    assert audit.order_from_transcript(cache, ROLLING_VTT) == [
        "Apple gave me an early look."
    ]


def test_a_cue_the_captions_do_not_contain_is_dropped():
    # An orphan left by an earlier parse has no place in the order, and guessing
    # one would put it beside a cue it never sat next to.
    cache = {"Apple gave me an early look.": "x", "not in this video at all": "y"}
    assert audit.order_from_transcript(cache, ROLLING_VTT) == [
        "Apple gave me an early look."
    ]


def test_pairs_falls_back_to_the_cache_when_cues_json_is_missing(tmp_path):
    # The shape of the video this check was written for: a translation cache and
    # the source captions, and neither of the two files the audit used to need.
    folder = tmp_path / "vid"
    folder.mkdir()
    (folder / "1080p.mp4.en.vtt").write_text(ROLLING_VTT)
    (folder / "narration.vi.json").write_text(json.dumps({
        "omniroute:sub_translation": {
            "Apple gave me an early look.": "Apple cho mình xem trước.",
            "The brand new AirPods 5 are here.": "AirPods 5 mới đã ra mắt.",
        }
    }))

    cues, translations = audit.pairs(str(folder))
    assert cues == [
        "The brand new AirPods 5 are here.",
        "Apple gave me an early look.",
    ]
    assert translations == ["AirPods 5 mới đã ra mắt.", "Apple cho mình xem trước."]


def test_pairs_does_not_mistake_the_vi_mt_subtitle_for_the_source(tmp_path):
    folder = tmp_path / "vid"
    folder.mkdir()
    (folder / "1080p.mp4.vi-mt.vtt").write_text(ROLLING_VTT)
    (folder / "narration.vi.json").write_text(json.dumps(
        {"omniroute:sub_translation": {"Apple gave me an early look.": "x"}}))

    # Only a vi-mt subtitle and no source captions: nothing to order against.
    assert audit.pairs(str(folder)) == ([], [])


# ---- the transcript has to be cleaned the way the cues were ------------------

ENTITY_VTT = """WEBVTT

00:00:00.000 --> 00:00:02.000
 
The AirPods 4 without&amp;c had a snug fit.

00:00:02.000 --> 00:00:04.000
The AirPods 4 without&amp;c had a snug fit.
&gt;&gt; Now, the A&amp;C is better.

00:00:04.000 --> 00:00:06.000
[music] So, should you upgrade?
"""


def test_an_entity_in_the_captions_does_not_lose_the_cue():
    # parseVTT decodes &amp; before the cue is ever cached, so a transcript read
    # raw does not contain the text any key was built from. Measured on
    # kXVt4atqMv8, this lost 25 of 183 cues — and a cue that cannot be placed is
    # a cue the audit never examines.
    cache = {"The AirPods 4 without&c had a snug fit.": "x"}
    assert audit.order_from_transcript(cache, ENTITY_VTT) == [
        "The AirPods 4 without&c had a snug fit."
    ]


def test_a_speaker_marker_does_not_lose_the_cue():
    cache = {"Now, the A&C is better.": "x"}
    assert audit.order_from_transcript(cache, ENTITY_VTT) == ["Now, the A&C is better."]


def test_a_bracketed_description_does_not_lose_the_cue():
    cache = {"So, should you upgrade?": "x"}
    assert audit.order_from_transcript(cache, ENTITY_VTT) == ["So, should you upgrade?"]
