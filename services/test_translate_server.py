"""Alignment tests for the batch translation endpoint.

The model is not exercised here. What is exercised is the part that decides
whether the model's answer may be trusted, which is the part that failed in
measurement.
"""
from translate_server import parse_numbered, aligned_or_none


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
