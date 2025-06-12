# Testing Strategy for `python_mcp_server.py`

## 1. Overview

Thorough testing is crucial to ensure the reliability, correctness, and robustness of the `python_mcp_server.py` application. This includes verifying the core tool execution logic, the Server-Sent Event (SSE) communication mechanism, interactions with external command-line tools, configuration handling, and error parsing. A multi-layered testing approach will be adopted.

## 2. Unit Tests

### 2.1. Core Logic Functions (`execute_..._tool`)

*   **Framework:** `pytest` is preferred for its concise syntax and rich plugin ecosystem, though `unittest` (with `unittest.mock`) is also suitable.
*   **Key Technique:** The primary technique will be mocking the `subprocess.run` function. This allows simulation of various scenarios:
    *   Successful command execution with expected `stdout`.
    *   Command execution resulting in errors (non-zero return codes) with `stderr` content.
    *   `FileNotFoundError` when a command (e.g., `swechaind`, `./bin/feedback`, `./bin/feedbackQA`) is not found.
    *   Variations in `stdout` that test parsing logic (e.g., valid JSON, invalid JSON, empty output).
    *   `subprocess.TimeoutExpired` to ensure timeouts are handled.
*   **Assertions:**
    *   Verify that the returned dictionaries match the expected structure for both success and error cases (e.g., presence of `status`, `data_type`, `data`, `message`, `details`, `returncode`).
    *   Ensure that parameters passed to `subprocess.run` are constructed correctly based on input parameters and values from the `CONFIG` dictionary.
    *   Confirm that `stdout` from mocked `subprocess.run` is correctly parsed (e.g., into JSON or returned as text).
    *   Validate that `KeyError` is handled for missing required parameters (though Pydantic handles this at the API layer, unit tests can ensure function robustness).
*   **Specific Tool Testing:**
    *   **`execute_feedbackqa_tool`**: Test with sample questions, mock the `./bin/feedbackQA` script's output (success/failure).
    *   **`execute_create_and_fund_tool`**:
        *   Mock `swechaind keys add` to simulate:
            *   Successful key creation.
            *   Key already exists (this should trigger a `swechaind keys show` call).
            *   Other errors during key creation.
        *   Mock `swechaind keys show` to simulate success and error when retrieving an existing key.
        *   Mock `swechaind tx bank send` to simulate successful funding and various funding errors.
        *   Test the complete logic flow for both new key creation and existing key scenarios.
    *   **`execute_open_auction_tool`, `execute_create_bid_tool`, `execute_close_auction_tool`, `execute_pay_tool`**:
        *   Mock the respective `swechaind tx issuemarket/...` or `swechaind tx bank/...` calls to simulate success and various blockchain-related errors.
        *   Ensure correct command construction with all parameters, including those from the `CONFIG` dictionary (e.g., `--chain-id`, `--fees`, `--keyring-backend`).

### 2.2. Error Parsing (`parse_swechaind_error`)

*   **Dedicated Tests:** Create specific unit tests for the `parse_swechaind_error` function.
*   **Test Cases:** Use a gallery of sample `stderr` string inputs covering all implemented parsing rules:
    *   Timeout (passed via `original_exception`).
    *   "key not found" (with and without identifiable names/addresses).
    *   "already exists" (in key creation context).
    *   "insufficient funds" (with and without address).
    *   "account sequence mismatch" (with and without sequence numbers).
    *   "invalid coins" / "invalid amount".
    *   "auction not found" / "auction does not exist".
    *   "auction is not open" / "auction is closed".
    *   "unauthorized".
    *   "bid does not exist".
    *   Default fallback message with and without `original_exception`.
*   **Assertions:** Verify that the function returns the expected user-friendly and actionable error string for each input.

## 3. Integration Tests (for FastAPI SSE Endpoints)

*   **Framework:** `pytest` will be used, leveraging an HTTP client like `httpx` for making requests to the FastAPI application. For handling SSE streams, libraries such as `httpx-sse` or `sseclient-py` are recommended. FastAPI's `TestClient` can be useful for initial endpoint accessibility checks and non-SSE aspects.
*   **Focus:**
    *   **Pydantic Validation:** Send invalid request bodies to all tool endpoints to ensure FastAPI returns a 422 Unprocessable Entity error as expected.
    *   **SSE Event Sequence & Content:**
        *   For each tool, verify that the correct sequence of events (`tool_started`, `tool_result` or `tool_error`, `tool_finished`) is received for both successful and failed tool executions.
        *   Inspect the `data` field of each SSE event to ensure it's valid JSON and contains the expected payload (e.g., correct `tool_name`, `result` structure for success, `error` structure for failures).
    *   **Error Reporting over SSE:** Simulate errors in the underlying tool execution (via mocking `subprocess.run` or by providing parameters that cause internal tool logic to fail) and verify that appropriate `tool_error` or `runtime_error` events (containing messages from `parse_swechaind_error` where applicable) are streamed back to the client.
    *   **Headers:** Check for correct SSE headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache`).
    *   **Idempotency Testing:** For `create-and-fund-address`, call the endpoint twice with the same parameters. Verify that the first call creates the key and funds, and the second call recognizes the key exists and still attempts to fund (or reports success if funding is also considered idempotent based on desired behavior).
*   **Subprocess Handling:**
    *   Initially, `subprocess.run` within the tool execution functions can be mocked to isolate the SSE streaming and FastAPI logic.
    *   For more comprehensive integration tests, tests could be designed to run against actual (but controlled) subprocesses if the test environment permits and setup complexity is managed. This is especially relevant for testing the correct invocation of `swechaind` with various flags.

## 4. Configuration Testing

*   **Strategy:** Primarily through integration tests or specialized unit tests that can modify the `CONFIG` dictionary before a tool executor is called (for some parameters) or by setting environment variables before server startup for test runs.
*   **Test Cases:**
    *   Run the server/tool executors with different valid and invalid environment variable settings for:
        *   `KEYRING_BACKEND`, `CHAIN_ID`, `DEFAULT_FEES` (verify these are used in `swechaind` commands by inspecting mocked `subprocess.run` calls or, in broader tests, by observing blockchain interactions if possible).
        *   `COMMAND_TIMEOUT_SECONDS` (verify that commands actually time out as per the setting and that shorter/longer timeouts are respected by mocking `subprocess.run` to delay and then checking for timeout-specific error messages).
        *   `SWECHAIND_PATH`, `FEEDBACK_BIN_PATH`, `FEEDBACK_QA_BIN_PATH` (verify that `FileNotFoundError` occurs if a path is invalid, or that a mock/test script at a specified path is called).
    *   Verify that default values for all configuration parameters are used when corresponding environment variables are unset.
    *   Test `COMMAND_TIMEOUT_SECONDS` with an invalid (non-integer) value to ensure the default is used and a warning is logged at startup.
    *   **Log Level Testing**: Verify that setting `MCP_SERVER_LOG_LEVEL` to different values (e.g., DEBUG, INFO, WARNING) correctly changes the verbosity of the server's log output. This can be checked by inspecting log files or console output for the presence/absence of messages at different severity levels during test runs. Ensure the "Logging level set to..." message at startup reflects the applied level or the default if an invalid value was provided.
    *   **Keyring Security Warning**: Ensure that the security warning message regarding `KEYRING_BACKEND='test'` is logged at server startup when this configuration is active, and that it does not appear when a different keyring backend (e.g., 'os', 'file', or an unset variable resulting in the default 'test') is specified.
*   **Assertions:** Server behavior changes as expected (e.g., command construction, timeout behavior, error messages for bad paths, log verbosity, presence/absence of specific startup warnings).

## 5. End-to-End (E2E) Tests (Optional)

*   **Environment:** These tests require a fully operational environment, including:
    *   A running instance of the `python_mcp_server.py` application.
    *   Live, accessible instances of `swechaind` (potentially a local testnet node with pre-configured accounts and state).
    *   The `./bin/feedback` and `./bin/feedbackQA` executables and any dependencies they have.
*   **Focus:**
    *   Verify the complete workflow from HTTP request to actual side-effects of tool operations (e.g., a balance changing after a `pay` operation, an auction appearing in `feedbackQA` after `open-auction`). This requires careful test data management and state reset/setup.
    *   Confirm that the SSE responses accurately reflect the outcomes from these live services.
*   **Complexity:** E2E tests are the most complex to set up, maintain, and run reliably due to their dependency on external systems. They provide the highest confidence but should be used judiciously for critical paths.

## 6. Tools & Libraries

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
