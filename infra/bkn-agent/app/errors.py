from fastapi import HTTPException


def err(status: int, code: str, description: str, detail: str = "", solution: str = "") -> HTTPException:
    return HTTPException(
        status_code=status,
        detail={
            "code": f"BknAgent.{code}",
            "description": description,
            "detail": detail or description,
            "solution": solution,
            "link": "",
        },
    )


def not_found(what: str, ident: str) -> HTTPException:
    return err(404, "NotFound", f"{what}不存在", f"{what} {ident} 不存在", "请检查 id 是否正确。")


def bad_request(code: str, description: str, detail: str = "", solution: str = "") -> HTTPException:
    return err(400, f"ParamError.{code}", description, detail, solution)
