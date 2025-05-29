# Testing Strategy for `python_mcp_server.py`

## 1. Overview

Thorough testing is crucial to ensure the reliability, correctness, and robustness of the `python_mcp_server.py` application. This includes verifying the core tool execution logic, the Server-Sent Event (SSE) communication mechanism, and interactions with external command-line tools. A multi-layered testing approach will be adopted.

## 2. Unit Tests (for Core Logic Functions - `execute_..._tool`)

*   **Framework:** `pytest` is preferred for its concise syntax and rich plugin ecosystem, though `unittest` (with `unittest.mock`) is also suitable.
*   **Key Technique:** The primary technique will be mocking the `subprocess.run` function. This allows simulation of various scenarios:
    *   Successful command execution with expected `stdout`.
    *   Command execution resulting in errors (non-zero return codes) with `stderr` content.
    *   `FileNotFoundError` when a command (e.g., `swechaind`, `./bin/feedback`) is not found.
    *   Variations in `stdout` that test parsing logic (e.g., valid JSON, invalid JSON, empty output).
*   **Assertions:**
    *   Verify that the returned dictionaries match the expected structure for both success and error cases (e.g., presence of `status`, `data_type`, `data`, `message`, `details`, `returncode`).
    *   Ensure that parameters passed to `subprocess.run` are constructed correctly based on input parameters.
    *   Confirm that `stdout` from mocked `subprocess.run` is correctly parsed (e.g., into JSON or returned as text).
    *   Validate that `KeyError` is handled for missing required parameters (though Pydantic handles this at the API layer, unit tests can ensure function robustness).

## 3. Integration Tests (for FastAPI SSE Endpoints)

*   **Framework:** `pytest` will be used, leveraging an HTTP client like `httpx` for making requests to the FastAPI application. For handling SSE streams, libraries such as `httpx-sse` or `sseclient-py` are recommended. FastAPI's `TestClient` can be useful for initial endpoint accessibility checks and non-SSE aspects.
*   **Focus:**
    *   **Pydantic Validation:** Send invalid request bodies to ensure FastAPI returns a 422 Unprocessable Entity error as expected.
    *   **SSE Event Sequence & Content:**
        *   Verify that the correct sequence of events (`tool_started`, `tool_result` or `tool_error`, `tool_finished`) is received.
        *   Inspect the `data` field of each SSE event to ensure it's valid JSON and contains the expected payload (e.g., correct `tool_name`, `result` structure for success, `error` structure for failures).
    *   **Error Reporting over SSE:** Simulate errors in the underlying tool execution (via mocking `subprocess.run` at this stage) and verify that appropriate `tool_error` or `runtime_error` events are streamed back to the client.
    *   **Headers:** Check for correct SSE headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache`).
*   **Subprocess Handling:**
    *   Initially, `subprocess.run` within the tool execution functions can be mocked to isolate the SSE streaming and FastAPI logic.
    *   For more comprehensive integration tests, tests could be designed to run against actual (but controlled) subprocesses if the test environment permits and setup complexity is managed.

## 4. End-to-End (E2E) Tests (Optional)

*   **Environment:** These tests require a fully operational environment, including:
    *   A running instance of the `python_mcp_server.py` application.
    *   Live, accessible instances of `swechaind` (potentially a local testnet node).
    *   The `./bin/feedback` executable and any dependencies it has.
*   **Focus:**
    *   Verify the complete workflow from HTTP request to actual side-effects of tool operations (e.g., a balance changing after a `send` operation, though this requires careful test data management).
    *   Confirm that the SSE responses accurately reflect the outcomes from these live services.
*   **Complexity:** E2E tests are the most complex to set up, maintain, and run reliably due to their dependency on external systems. They provide the highest confidence but should be used judiciously for critical paths.

## 5. Tools & Libraries

The following tools and libraries are recommended for implementing the testing strategy:

*   **Test Runner:**
    *   `pytest`
*   **Mocking:**
    *   `unittest.mock` (standard library, often used with `pytest`)
    *   `pytest-mock` (pytest plugin for `unittest.mock`)
*   **HTTP Client (for Integration/E2E Tests):**
    *   `httpx` (modern async HTTP client)
*   **SSE Client Libraries (for Integration/E2E Tests):**
    *   `httpx-sse` (SSE support for `httpx`)
    *   `sseclient-py` (alternative SSE client)
*   **FastAPI Testing Utilities:**
    *   `TestClient` (from `fastapi.testclient`, for synchronous testing of FastAPI apps)
