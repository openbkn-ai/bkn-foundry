"""Tests for test_commons_response."""
import pytest
from fastapi import status
from fastapi.responses import JSONResponse
from app.commons.response import error_response, correct_response


class TestErrorResponse:
    """Tests for test error response."""

    def test_error_response_basic(self):
        """Test test error response basic."""
        response = error_response(
            status_code=400,
            code="TEST.Error",
            detail="测试错误详情",
            language="zh"
        )
        assert isinstance(response, JSONResponse)
        assert response.status_code == 400

    def test_error_response_with_solution(self):
        """Test test error response with solution."""
        response = error_response(
            status_code=500,
            code="TEST.ServerError",
            detail="服务器错误",
            solution="请联系管理员",
            language="zh"
        )
        assert isinstance(response, JSONResponse)
        assert response.status_code == 500

    def test_error_response_with_link(self):
        """Test test error response with link."""
        response = error_response(
            status_code=404,
            code="TEST.NotFound",
            detail="资源未找到",
            link="http://help.example.com",
            language="zh"
        )
        assert isinstance(response, JSONResponse)
        assert response.status_code == 404

    def test_error_response_all_params(self):
        """Test test error response all params."""
        response = error_response(
            status_code=403,
            code="TEST.Forbidden",
            detail="权限不足",
            solution="请申请权限",
            link="http://help.example.com",
            language="zh"
        )
        assert isinstance(response, JSONResponse)
        assert response.status_code == 403

    def test_error_response_english(self):
        """Test test error response english."""
        response = error_response(
            status_code=400,
            code="TEST.Error",
            detail="Test error detail",
            language="en"
        )
        assert isinstance(response, JSONResponse)
        assert response.status_code == 400


class TestCorrectResponse:
    """Tests for test correct response."""

    def test_correct_response_default(self):
        """Test test correct response default."""
        data = {"message": "success"}
        response = correct_response(data=data)
        assert isinstance(response, JSONResponse)
        assert response.status_code == 200

    def test_correct_response_custom_status(self):
        """Test test correct response custom status."""
        data = {"id": "123", "status": "created"}
        response = correct_response(http_code=201, data=data)
        assert isinstance(response, JSONResponse)
        assert response.status_code == 201

    def test_correct_response_none_data(self):
        """Test test correct response none data."""
        response = correct_response(data=None)
        assert isinstance(response, JSONResponse)
        assert response.status_code == 200

    def test_correct_response_list_data(self):
        """Test test correct response list data."""
        data = [{"id": 1}, {"id": 2}]
        response = correct_response(data=data)
        assert isinstance(response, JSONResponse)
        assert response.status_code == 200

    def test_correct_response_dict_data(self):
        """Test test correct response dict data."""
        data = {"count": 10, "items": []}
        response = correct_response(data=data)
        assert isinstance(response, JSONResponse)
        assert response.status_code == 200

    def test_correct_response_202_accepted(self):
        """Test test correct response 202 accepted."""
        data = {"task_id": "abc123"}
        response = correct_response(http_code=status.HTTP_202_ACCEPTED, data=data)
        assert isinstance(response, JSONResponse)
        assert response.status_code == 202

    def test_correct_response_204_no_content(self):
        """Test test correct response 204 no content."""
        response = correct_response(http_code=status.HTTP_204_NO_CONTENT, data=None)
        assert isinstance(response, JSONResponse)
        assert response.status_code == 204

