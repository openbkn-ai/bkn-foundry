#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# Copyright The kweaver.ai Authors.
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.
import pytest
from server.utils.version import (
    compare_version,
    get_max_version,
    get_min_version,
    sort_versions,
    is_version_dir,
    extract_number,
    VersionUtil,
)


class TestCompareVersion:
    def test_equal(self):
        assert compare_version("1.0.0", "1.0.0") == 0

    def test_patch_less(self):
        assert compare_version("1.0.0", "1.0.1") == -1

    def test_patch_greater(self):
        assert compare_version("1.0.1", "1.0.0") == 1

    def test_minor_greater(self):
        assert compare_version("1.10.0", "1.9.0") == 1

    def test_major_greater(self):
        assert compare_version("2.0.0", "1.9.9") == 1

    def test_unequal_length_equal(self):
        # 1.4 == 1.4.0
        assert compare_version("1.4", "1.4.0") == 0

    def test_unequal_length_less(self):
        assert compare_version("1.4", "1.4.1") == -1

    def test_non_numeric_raises(self):
        with pytest.raises(Exception):
            compare_version("v1.0.0", "1.0.0")


class TestSortVersions:
    def test_basic_sort(self):
        assert sort_versions(["1.2.0", "0.9.0", "1.0.0"]) == ["0.9.0", "1.0.0", "1.2.0"]

    def test_numeric_not_lexicographic(self):
        # 字典序会把 1.10 排在 1.9 前，数字序应排在后
        result = sort_versions(["1.10.0", "1.9.0", "1.2.0"])
        assert result == ["1.2.0", "1.9.0", "1.10.0"]

    def test_single_element(self):
        assert sort_versions(["1.0.0"]) == ["1.0.0"]

    def test_empty(self):
        assert sort_versions([]) == []

    def test_already_sorted(self):
        versions = ["0.1.0", "0.2.0", "1.0.0"]
        assert sort_versions(versions) == versions

    def test_duplicates(self):
        assert sort_versions(["1.0.0", "1.0.0"]) == ["1.0.0", "1.0.0"]


class TestGetMaxMinVersion:
    def test_max(self):
        assert get_max_version(["1.0.0", "2.0.0", "1.5.0"]) == "2.0.0"

    def test_min(self):
        assert get_min_version(["1.0.0", "2.0.0", "1.5.0"]) == "1.0.0"

    def test_single(self):
        assert get_max_version(["3.0.0"]) == "3.0.0"
        assert get_min_version(["3.0.0"]) == "3.0.0"

    def test_empty_returns_none(self):
        assert get_max_version([]) is None
        assert get_min_version([]) is None

    def test_max_with_unequal_length(self):
        assert get_max_version(["1.4", "1.4.1"]) == "1.4.1"


class TestIsVersionDir:
    def test_valid_two_parts(self):
        assert is_version_dir("1.0") is True

    def test_valid_three_parts(self):
        assert is_version_dir("1.0.0") is True

    def test_valid_four_parts(self):
        assert is_version_dir("1.4.20.1") is True

    def test_invalid_prefix(self):
        assert is_version_dir("v1.0.0") is False

    def test_invalid_alpha(self):
        assert is_version_dir("1.0.0-beta") is False

    def test_plain_word(self):
        assert is_version_dir("latest") is False

    def test_empty(self):
        assert is_version_dir("") is False


class TestExtractNumber:
    def test_single_digit(self):
        assert extract_number("01-add-column.sql") == 1

    def test_double_digit(self):
        assert extract_number("12-rename-index.py") == 12

    def test_json_extension(self):
        assert extract_number("03-alter-table.json") == 3

    def test_full_path(self):
        assert extract_number("repos/svc/mariadb/1.0.0/05-data.sql") == 5

    def test_init_sql_raises(self):
        with pytest.raises(Exception):
            extract_number("init.sql")

    def test_no_prefix_raises(self):
        with pytest.raises(Exception):
            extract_number("add-column.sql")


class TestVersionUtil:
    def test_less_than(self):
        assert VersionUtil("1.0.0") < VersionUtil("1.0.1")

    def test_not_less_than_equal(self):
        assert not (VersionUtil("1.0.0") < VersionUtil("1.0.0"))

    def test_greater_equal(self):
        assert VersionUtil("1.0.1") >= VersionUtil("1.0.0")
        assert VersionUtil("1.0.0") >= VersionUtil("1.0.0")

    def test_equal(self):
        assert VersionUtil("1.0.0") == VersionUtil("1.0.0")
        assert VersionUtil("1.4") == VersionUtil("1.4.0")

    def test_sorted_builtin(self):
        versions = [VersionUtil("1.10.0"), VersionUtil("1.9.0"), VersionUtil("1.2.0")]
        result = [str(v) for v in sorted(versions)]
        assert result == ["1.2.0", "1.9.0", "1.10.0"]

    def test_str(self):
        assert str(VersionUtil("2.3.1")) == "2.3.1"

    def test_hash_usable_in_set(self):
        s = {VersionUtil("1.0.0"), VersionUtil("1.0.0")}
        assert len(s) == 1
