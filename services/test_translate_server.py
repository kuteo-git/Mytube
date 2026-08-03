"""Alignment tests for the batch translation endpoint.

The model is not exercised here. What is exercised is the part that decides
whether the model's answer may be trusted, which is the part that failed in
measurement.
"""
from translate_server import (
    OMNIROUTE_API_KEY,
    OMNIROUTE_BASE_URL,
    OMNIROUTE_MODEL,
    aligned_or_none,
    openai_content,
    parse_numbered,
    resolve_config,
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
