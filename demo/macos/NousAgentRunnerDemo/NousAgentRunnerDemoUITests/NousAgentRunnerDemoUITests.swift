import XCTest
#if canImport(AppKit)
import AppKit
#endif

final class NousAgentRunnerDemoUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    func testE2E_DemoCoreFlows() throws {
        let app = XCUIApplication()
        addTeardownBlock {
            if app.state == .runningForeground {
                let settingsClose = app.buttons["settingsCloseButton"]
                if settingsClose.exists { settingsClose.click() }

                let delete = app.buttons["deleteServiceButton"]
                if delete.exists {
                    delete.click()
                    let deadline = Date().addingTimeInterval(10)
                    while Date() < deadline {
                        if !app.buttons["stopServiceButton"].exists { break }
                        RunLoop.current.run(until: Date().addingTimeInterval(0.2))
                    }
                }
            }
            app.terminate()
        }
        app.launch()

        let refreshStatus = app.buttons["refreshStatusButton"]
        XCTAssertTrue(refreshStatus.waitForExistence(timeout: 30), "missing Refresh Status button")
        refreshStatus.click()

        let statusText = app.staticTexts["statusText"]
        XCTAssertTrue(statusText.waitForExistence(timeout: 30), "missing status text")
        waitUntilStatusNotEqual(statusText, forbidden: "Not loaded", timeout: 60)
        waitUntilStatusNotPrefixed(statusText, forbiddenPrefix: "Runner error:", timeout: 60)
        waitUntilStatusNotPrefixed(statusText, forbiddenPrefix: "Error:", timeout: 60)
        waitUntilTextContains(statusText, needle: "protocols", timeout: 60)
        waitUntilTextContains(statusText, needle: "asmp", timeout: 60)
        waitUntilTextContains(statusText, needle: "asp", timeout: 60)

        let testTunnel = app.buttons["testTunnelButton"]
        XCTAssertTrue(testTunnel.waitForExistence(timeout: 10), "missing Test Guest→Host Tunnel button")
        testTunnel.click()
        waitUntilTextContainsAny(
            statusText,
            needles: ["guest→host tunnel OK", "guest→host tunnel FAILED"],
            timeout: 600
        )
        XCTAssertTrue(
            elementContainsText(statusText, needle: "guest→host tunnel OK"),
            "guest→host tunnel failed: label=\(String(statusText.label.prefix(300))) value=\(String(describing: statusText.value))"
        )

        let refreshServices = app.buttons["refreshServicesButton"]
        XCTAssertTrue(refreshServices.waitForExistence(timeout: 30), "missing Refresh Services button")
        refreshServices.click()

        let skills = app.buttons["skillsButton"]
        XCTAssertTrue(skills.waitForExistence(timeout: 10), "missing Skills button")
        skills.click()
        let skillsClose = app.buttons["skillsCloseButton"]
        XCTAssertTrue(skillsClose.waitForExistence(timeout: 10), "missing Skills close button")
        let skillsStatus = app.staticTexts["skillsStatusText"]
        if skillsStatus.waitForExistence(timeout: 2) {
            XCTAssertFalse(skillsStatus.label.hasPrefix("Error:"), "skills error: \(skillsStatus.label)")
        }
        skillsClose.click()

        let settings = app.buttons["settingsButton"]
        XCTAssertTrue(settings.waitForExistence(timeout: 10), "missing Settings button")
        settings.click()

        let envEditor = app.textViews["serviceEnvEditor"]
        XCTAssertTrue(envEditor.waitForExistence(timeout: 10), "missing service env editor")
        app.buttons["settingsCloseButton"].click()

        let imageRef = app.textFields["imageRefField"]
        XCTAssertTrue(imageRef.waitForExistence(timeout: 10), "missing image_ref field")
        replaceText(imageRef, with: "docker.io/gravtice/nous-claude-agent-service:0.2.10")

        let create = app.buttons["createServiceButton"]
        XCTAssertTrue(create.waitForExistence(timeout: 30), "missing Create Service button")
        create.click()

        let stop = app.buttons["stopServiceButton"]
        waitUntilServiceCreatedOrFail(statusText: statusText, stopButton: stop, timeout: 900)

        let serviceStateText = app.staticTexts["serviceStateText"]
        XCTAssertTrue(serviceStateText.waitForExistence(timeout: 30), "missing service_state text")
        waitUntilTextContains(serviceStateText, needle: "running", refreshButton: refreshServices, timeout: 900)

        let interrupt = app.buttons["interruptButton"]
        XCTAssertTrue(interrupt.waitForExistence(timeout: 60), "missing Interrupt button")
        waitUntilEnabled(interrupt, timeout: 900)

        let debugEvents = app.staticTexts["debugEventsText"]
        XCTAssertTrue(debugEvents.waitForExistence(timeout: 10), "missing debug events text")

        let permissionPicker = app.popUpButtons["permissionModePicker"]
        XCTAssertTrue(permissionPicker.waitForExistence(timeout: 10), "missing permission mode picker")
        let permissionApply = app.buttons["permissionModeApplyButton"]
        XCTAssertTrue(permissionApply.waitForExistence(timeout: 10), "missing permission mode apply button")

        permissionPicker.click()
        app.menuItems["plan"].click()
        permissionApply.click()
        waitUntilTextContains(debugEvents, needle: "permission_mode.updated", timeout: 60)

        permissionPicker.click()
        app.menuItems["bypassPermissions"].click()
        permissionApply.click()

        let input = app.textFields["chatInputField"]
        XCTAssertTrue(input.waitForExistence(timeout: 30), "missing chat input field")

        let token = "ui_xcuitest_\(Int(Date().timeIntervalSince1970))"
        replaceText(input, with: "请只输出下面这串 token（不要添加其它文字/标点/换行）：\(token)")

        let send = app.buttons["sendButton"]
        XCTAssertTrue(send.waitForExistence(timeout: 10), "missing Send button")
        send.click()

        let output = app.staticTexts["chatOutputText"]

        let usage = app.staticTexts["usageSummaryText"]
        XCTAssertTrue(usage.waitForExistence(timeout: 900), "timeout waiting for usage summary (real model call)")
        waitUntilTextContains(output, needle: token, timeout: 900)

        stop.click()
        waitUntilTextContains(serviceStateText, needle: "stopped", refreshButton: refreshServices, timeout: 300)

        let resume = app.buttons["resumeServiceButton"]
        XCTAssertTrue(resume.waitForExistence(timeout: 10), "missing Resume Service button")
        resume.click()
        waitUntilTextContains(serviceStateText, needle: "running", refreshButton: refreshServices, timeout: 900)

        let connectWS = app.buttons["connectWSButton"]
        XCTAssertTrue(connectWS.waitForExistence(timeout: 10), "missing Connect WS button")
        connectWS.click()

        let token2 = "ui_xcuitest_resume_\(Int(Date().timeIntervalSince1970))"
        replaceText(input, with: "请只输出下面这串 token（不要添加其它文字/标点/换行）：\(token2)")
        send.click()
        waitUntilTextContains(output, needle: token2, timeout: 900)

        let delete = app.buttons["deleteServiceButton"]
        XCTAssertTrue(delete.waitForExistence(timeout: 10), "missing Delete Service button")
        delete.click()
        waitUntilNotExists(app.buttons["stopServiceButton"], timeout: 120)
    }

    private func waitUntilEnabled(_ el: XCUIElement, timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if el.isEnabled { return }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTFail("timeout waiting for element to become enabled: \(el)")
    }

    private func waitUntilNotExists(_ el: XCUIElement, timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !el.exists { return }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTFail("timeout waiting for element to disappear: \(el)")
    }

    private func waitUntilTextContains(_ el: XCUIElement, needle: String, timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if elementContainsText(el, needle: needle) { return }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTFail("timeout waiting for text to contain \(needle). label=\(String(el.label.prefix(200))) value=\(String(describing: el.value))")
    }

    private func waitUntilTextContainsAny(_ el: XCUIElement, needles: [String], timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            for n in needles {
                if elementContainsText(el, needle: n) { return }
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTFail("timeout waiting for text to contain any of: \(needles). label=\(String(el.label.prefix(200))) value=\(String(describing: el.value))")
    }

    private func waitUntilTextContains(_ el: XCUIElement, needle: String, refreshButton: XCUIElement, timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if elementContainsText(el, needle: needle) { return }
            refreshButton.click()
            RunLoop.current.run(until: Date().addingTimeInterval(1.0))
        }
        XCTFail("timeout waiting for text to contain \(needle). label=\(String(el.label.prefix(200))) value=\(String(describing: el.value))")
    }

    private func elementContainsText(_ el: XCUIElement, needle: String) -> Bool {
        if el.label.contains(needle) { return true }
        if let v = el.value as? String, v.contains(needle) { return true }
        return false
    }

    private func waitUntilStatusNotEqual(_ el: XCUIElement, forbidden: String, timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if el.label != forbidden { return }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTFail("status still equals \(forbidden)")
    }

    private func waitUntilStatusNotPrefixed(_ el: XCUIElement, forbiddenPrefix: String, timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !el.label.hasPrefix(forbiddenPrefix) { return }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTFail("status still starts with \(forbiddenPrefix)")
    }

    private func waitUntilServiceCreatedOrFail(statusText: XCUIElement, stopButton: XCUIElement, timeout: TimeInterval) {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if stopButton.exists { return }

            let status = statusText.label
            if status.hasPrefix("Runner error:") || status.hasPrefix("Error:") {
                XCTFail("create service failed: \(String(status.prefix(500)))")
                return
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        XCTFail("timeout waiting for service creation (Stop button). last status: \(String(statusText.label.prefix(500)))")
    }

    private func replaceText(_ el: XCUIElement, with text: String) {
        el.click()
        el.typeKey("a", modifierFlags: [.command])
#if canImport(AppKit)
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(text, forType: .string)
        el.typeKey("v", modifierFlags: [.command])
#else
        el.typeText(text)
#endif
    }
}
