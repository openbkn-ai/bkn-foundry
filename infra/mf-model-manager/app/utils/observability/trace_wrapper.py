# -*-coding:utf-8-*-
import asyncio
from opentelemetry import trace
from opentelemetry.trace import SpanKind, Tracer
from functools import wraps
from typing import Optional, Callable, AsyncGenerator, Any, Awaitable
from opentelemetry.trace import SpanKind, Tracer, Status, StatusCode
from app.utils.common import func_judgment

# The AnyRobot tracer was removed; use OTel's default no-op tracer when no provider exists.
tracer = trace.get_tracer(__name__)



def internal_span(
    name: Optional[str] = None,
    attributes: Optional[dict] = None,
) -> Callable:
    """
    Create a decorator that automatically generates an OpenTelemetry INTERNAL span.
    
    Args:
        name: Span name; defaults to the decorated function name.
        attributes: Attribute dictionary to add to the span.
        tracer_provider: Optional tracer provider instance.
        
    Returns:
        The wrapped function.
    """
    def decorator(func: Callable) -> Callable:
        
        # Use the function name when no span name is provided.
        span_name = name or func.__name__
        is_async, is_stream = func_judgment(func)
        # if asyncio.iscoroutinefunction(func):
        if is_async and is_stream:
            # Handle asynchronous generator functions.
            @wraps(func)
            async def async_generator_wrapper(*args, **kwargs) -> AsyncGenerator[Any, Any]:
                with tracer.start_as_current_span(
                    span_name,
                    kind=SpanKind.INTERNAL,
                    attributes=attributes
                ) as span:
                    try:
                        print("..................")
                        span.set_status(Status(StatusCode.OK))
                        span.set_attribute("error", False)

                        kwargs["span"] = span
                        
                        result = func(*args, **kwargs)
                        async for item in result:
                            yield item
                       
                                                  
                    except Exception as e:
                        if span.is_recording():
                            span.set_status(Status(StatusCode.ERROR))
                            span.set_attribute("error", True)
                            span.record_exception(e)
                        raise
            return async_generator_wrapper
        elif is_async:
            # Handle asynchronous functions.
            @wraps(func)
            async def async_wrapper(*args, **kwargs) -> Awaitable[Any]:
                with tracer.start_as_current_span(
                    span_name,
                    kind=SpanKind.INTERNAL,
                    attributes=attributes
                ) as span:
                    try:

                        span.set_status(Status(StatusCode.OK))
                        span.set_attribute("error", False)

                        kwargs["span"] = span
                        
                        result = await func(*args, **kwargs)
                                                  
                    except Exception as e:
                        if span.is_recording():
                            span.set_status(Status(StatusCode.ERROR))
                            span.set_attribute("error", True)
                            span.record_exception(e)
                        raise
            return async_generator_wrapper
        else:
            @wraps(func)
            def sync_wrapper(*args, **kwargs) -> Any:
                # Create an INTERNAL span.
                with tracer.start_as_current_span(
                    span_name,
                    kind=SpanKind.INTERNAL,
                    attributes=attributes

                ) as span:
                    try:
                        kwargs["span"] = span
                        print("sync..............")
                        # Execute the decorated function.
                        result = func(*args, **kwargs)
                        span.set_status(Status(StatusCode.OK))
                        return result
                    except Exception as e:
                        # Record exception details.
                        if span.is_recording():
                            span.set_status(Status(StatusCode.ERROR))
                            span.set_attribute("error", True)
                            span.record_exception(e)
                        raise  # Re-raise the exception without changing existing behavior.
            return sync_wrapper
    return decorator
