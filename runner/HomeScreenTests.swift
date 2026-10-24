import XCTest

final class HomeScreenTests: XCTestCase {
    struct Command: Decodable {
        let action: String
        let x: Double?
        let y: Double?
        let toX: Double?
        let toY: Double?
        let duration: Double?
        let endHold: Double?
        let text: String?
        let name: String?
        let appBundle: String?
        let count: Int?
        let roundTrip: Bool?
        let firstPage: Bool?
        let page: Int?
        let keepPages: Int?
    }

    @MainActor
    func testCommand() throws {
        continueAfterFailure = false
        let json = try XCTUnwrap(ProcessInfo.processInfo.environment["HSC_COMMAND"])
        let command = try JSONDecoder().decode(Command.self, from: Data(json.utf8))
        let springboard = XCUIApplication(bundleIdentifier: command.appBundle ?? "com.apple.springboard")
        func point(_ x: Double?, _ y: Double?) throws -> XCUICoordinate {
            let x = try XCTUnwrap(x), y = try XCTUnwrap(y)
            guard (0...1).contains(x), (0...1).contains(y) else {
                throw NSError(domain: "HomeScreenPOC", code: 1,
                              userInfo: [NSLocalizedDescriptionKey: "Coordinates must be between 0 and 1"])
            }
            return springboard.coordinate(withNormalizedOffset: CGVector(dx: x, dy: y))
        }
        func capture(_ name: String) {
            let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
            attachment.name = name
            attachment.lifetime = .keepAlways
            add(attachment)
        }
        capture("before")
        switch command.action {
        case "move-icon":
            XCUIDevice.shared.press(.home)
            Thread.sleep(forTimeInterval: 0.5)
            XCUIDevice.shared.press(.home)
            Thread.sleep(forTimeInterval: 0.5)
            let name = try XCTUnwrap(command.name)
            func visibleIcon() throws -> XCUIElement {
                let matches = springboard.icons.matching(identifier: name)
                    .allElementsBoundByIndex.filter { $0.isHittable }
                XCTAssertEqual(matches.count, 1, "Expected exactly one visible icon with this name")
                return try XCTUnwrap(matches.first)
            }
            func frames() throws -> [String: CGRect] {
                var result: [String: CGRect] = [:]
                let bounds = springboard.frame
                func walk(_ snapshot: XCUIElementSnapshot) {
                    let frame = snapshot.frame
                    if snapshot.elementType == .icon && frame.width > 0 &&
                        bounds.contains(CGPoint(x: frame.midX, y: frame.midY)) {
                        let key = "\(snapshot.label)|\(Int((frame.minX / 3).rounded()))|\(Int((frame.minY / 3).rounded()))"
                        result[key] = frame
                    }
                    for child in snapshot.children { walk(child) }
                }
                walk(try springboard.snapshot())
                return result
            }
            let original = try visibleIcon().frame
            let baseline = try frames()
            let destination = try point(command.toX, command.toY)
            XCTAssertGreaterThan(hypot(destination.screenPoint.x - original.midX,
                                       destination.screenPoint.y - original.midY), 30,
                                 "Destination must differ from the current icon position")
            for frame in baseline.values where frame != original {
                let centerZone = CGRect(x: frame.midX - 22, y: frame.minY + 12,
                                        width: 44, height: min(50, frame.height - 12))
                XCTAssertFalse(centerZone.contains(destination.screenPoint),
                               "Destination overlaps an icon center and may create a folder; use a slot edge")
            }
            // Enter edit mode before dragging to avoid opening an icon context menu.
            try point(0.5, 0.79).press(forDuration: 2)
            XCTAssertTrue(springboard.buttons["Done"].waitForExistence(timeout: 5))
            defer { XCUIDevice.shared.press(.home) }
            try visibleIcon().coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
                .press(forDuration: 0.2, thenDragTo: destination,
                       withVelocity: .slow, thenHoldForDuration: 0.5)
            Thread.sleep(forTimeInterval: 1)
            let moved = try visibleIcon().frame
            capture("move-icon-result")
            XCTAssertGreaterThan(hypot(moved.midX - original.midX, moved.midY - original.midY), 30,
                                 "Icon did not move")
            XCTAssertLessThan(hypot(moved.midX - destination.screenPoint.x,
                                    moved.midY - destination.screenPoint.y), 60,
                              "Icon did not land near the requested position")
            if command.roundTrip == true {
                let origin = springboard.coordinate(withNormalizedOffset: .zero)
                    .withOffset(CGVector(dx: original.minX + 7 - springboard.frame.minX,
                                         dy: original.minY + 35 - springboard.frame.minY))
                try visibleIcon().coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
                    .press(forDuration: 0.2, thenDragTo: origin,
                           withVelocity: .slow, thenHoldForDuration: 0.5)
                XCUIDevice.shared.press(.home)
                Thread.sleep(forTimeInterval: 1)
                let restored = try frames()
                XCTAssertEqual(Set(restored.keys), Set(baseline.keys),
                               "Visible icon positions differ after round trip; inspect evidence")
                capture("move-icon-restored")
            }
        case "remove-hidden-pages":
            let keep = command.keepPages ?? 1
            XCTAssertTrue((1...15).contains(keep))
            // Requires the page overview, with all unwanted pages already hidden.
            let pageQuery = springboard.otherElements.matching(
                NSPredicate(format: "identifier MATCHES %@", "Page [0-9]+"))
            func confirmPageRemoval() throws {
                for _ in 0..<2 {
                    let alert = springboard.alerts.firstMatch
                    if !alert.exists { return }
                    let title = alert.label
                    let text = title + " " + alert.staticTexts.allElementsBoundByIndex.map { $0.label }.joined(separator: " ")
                    XCTAssertTrue((title.contains("Remove This Page") || title.contains("Keep Bookmarks")) && text.contains("App Library"),
                                  "Only page-removal and its bookmark confirmation are allowed")
                    capture("page-removal-confirmation")
                    let remove = alert.buttons["Remove"]
                    XCTAssertTrue(remove.exists)
                    remove.tap()
                    XCTAssertTrue(springboard.alerts[title].waitForNonExistence(timeout: 5))
                    Thread.sleep(forTimeInterval: 0.5)
                }
                XCTAssertFalse(springboard.alerts.firstMatch.exists)
            }
            if !springboard.alerts.firstMatch.exists && pageQuery.allElementsBoundByIndex.filter({ $0.frame.width > 50 }).isEmpty {
                XCUIDevice.shared.press(.home)
                Thread.sleep(forTimeInterval: 0.5)
                XCUIDevice.shared.press(.home)
                Thread.sleep(forTimeInterval: 0.5)
                try point(0.5, 0.79).press(forDuration: 2)
                XCTAssertTrue(springboard.buttons["Done"].waitForExistence(timeout: 5))
                springboard.pageIndicators["Page control"].tap()
            }
            // Resume a pending page/bookmark confirmation from a prior interrupted run.
            if springboard.alerts.firstMatch.exists { try confirmPageRemoval() }
            func activePages() -> [XCUIElement] {
                pageQuery.allElementsBoundByIndex.filter { $0.frame.width > 50 }
            }
            XCTAssertEqual(activePages().filter { ($0.value as? String) == "Visible" }.count, keep,
                           "Requested pages must remain visible")
            for _ in 0..<15 {
                let pages = activePages()
                if pages.count == keep { break }
                XCTAssertTrue((2...15).contains(pages.count))
                let hidden = pages.filter { ($0.value as? String) == "Hidden" }
                    .map { (element: $0, frame: $0.frame) }.sorted {
                        if abs($0.frame.minY - $1.frame.minY) > 5 { return $0.frame.minY < $1.frame.minY }
                        return $0.frame.minX < $1.frame.minX
                    }
                let page = try XCTUnwrap(hidden.first?.element, "Only hidden pages may be removed")
                XCTAssertFalse(page.buttons["toggle page hidden checkbox"].isSelected)
                for _ in 0..<5 {
                    if page.frame.minY > 100 && page.frame.minY < 750 { break }
                    let below = page.frame.minY >= 750
                    try point(0.5, below ? 0.75 : 0.30).press(forDuration: 0.05,
                        thenDragTo: point(0.5, below ? 0.30 : 0.75), withVelocity: .fast, thenHoldForDuration: 0)
                    Thread.sleep(forTimeInterval: 0.5)
                }
                let frame = page.frame
                XCTAssertTrue(frame.minY > 80 && frame.minY < 850 && frame.minX > 0)
                // iOS's minus control is drawn at the thumbnail's upper-left corner
                // but is not exposed as a separate accessible button on this device.
                page.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 3, dy: 3)).tap()
                let alert = springboard.alerts.firstMatch
                XCTAssertTrue(alert.waitForExistence(timeout: 5))
                try confirmPageRemoval()
                Thread.sleep(forTimeInterval: 0.6)
                XCTAssertEqual(activePages().count, pages.count - 1, "Expected one page to be removed")
            }
            XCTAssertEqual(activePages().count, keep)
            capture("retained-pages-overview")
            springboard.buttons["Done"].tap()
            if springboard.buttons["Done"].exists { springboard.buttons["Done"].tap() }
            XCUIDevice.shared.press(.home)
        case "hide-other-pages":
            let keep = command.keepPages ?? 1
            XCTAssertTrue((1...15).contains(keep))
            let first = springboard.otherElements["Page 1"].buttons["toggle page hidden checkbox"]
            if !first.exists {
                XCUIDevice.shared.press(.home)
                Thread.sleep(forTimeInterval: 0.5)
                XCUIDevice.shared.press(.home)
                Thread.sleep(forTimeInterval: 0.5)
                try point(0.5, 0.79).press(forDuration: 2)
                XCTAssertTrue(springboard.buttons["Done"].waitForExistence(timeout: 5))
                springboard.pageIndicators["Page control"].tap()
            }
            XCTAssertTrue(first.waitForExistence(timeout: 5))
            XCTAssertTrue(first.isSelected, "Keep the first page visible")
            let pageNames = springboard.otherElements.matching(
                NSPredicate(format: "identifier MATCHES %@", "Page [0-9]+"))
                .allElementsBoundByIndex.filter { $0.frame.width > 50 }.map { $0.identifier }.sorted {
                    (Int($0.split(separator: " ").last!) ?? 0) < (Int($1.split(separator: " ").last!) ?? 0)
                }
            XCTAssertTrue((1...15).contains(pageNames.count))
            XCTAssertGreaterThanOrEqual(pageNames.count, keep)
            for name in pageNames {
                let shouldKeep = (Int(name.split(separator: " ").last!) ?? 0) <= keep
                let toggle = springboard.otherElements[name].buttons["toggle page hidden checkbox"]
                if toggle.isSelected != shouldKeep {
                    for _ in 0..<5 {
                        if toggle.isHittable && toggle.frame.maxY < 880 { break }
                        try point(0.5, 0.75).press(forDuration: 0.05,
                            thenDragTo: point(0.5, 0.30), withVelocity: .fast, thenHoldForDuration: 0)
                        Thread.sleep(forTimeInterval: 0.5)
                    }
                    XCTAssertTrue(toggle.isHittable, "Page checkbox must be visible")
                    toggle.tap()
                    XCTAssertEqual(toggle.isSelected, shouldKeep, "Page visibility must match the requested range")
                }
            }
            let selected = springboard.buttons.matching(identifier: "toggle page hidden checkbox")
                .allElementsBoundByIndex.filter { $0.isSelected }
            XCTAssertEqual(selected.count, keep, "Requested Home Screen pages should remain visible")
            capture("requested-pages-selected")
            springboard.buttons["Done"].tap()
            if springboard.buttons["Done"].exists { springboard.buttons["Done"].tap() }
            XCUIDevice.shared.press(.home)
        case "widget-gallery":
            XCUIDevice.shared.press(.home)
            Thread.sleep(forTimeInterval: 0.5)
            XCUIDevice.shared.press(.home)
            Thread.sleep(forTimeInterval: 0.5)
            try point(0.5, 0.79).press(forDuration: 2)
            XCTAssertTrue(springboard.buttons["Done"].waitForExistence(timeout: 5))
            let indicator = springboard.pageIndicators["Page control"]
            let value = try XCTUnwrap(indicator.value as? String)
            let numbers = value.split(whereSeparator: { !$0.isNumber }).compactMap { Int($0) }
            XCTAssertEqual(numbers.count, 2, "Expected page indicator with current and total page counts")
            let total = try XCTUnwrap(numbers.last)
            XCTAssertTrue((1...15).contains(total))
            if command.firstPage != true {
            for _ in 1..<total {
                try point(0.9, 0.78).press(forDuration: 0.05,
                    thenDragTo: point(0.1, 0.78), withVelocity: .fast, thenHoldForDuration: 0)
                Thread.sleep(forTimeInterval: 0.4)
            }
            XCTAssertEqual(indicator.value as? String, "Page \(total) of \(total)")
            }
            springboard.buttons["Edit"].tap()
            let add = springboard.buttons["Add Widget"]
            XCTAssertTrue(add.waitForExistence(timeout: 5))
            add.tap()
        case "tap-button":
            let name = try XCTUnwrap(command.name)
            XCTAssertNotEqual(name, "Delete App", "This POC does not uninstall applications")
            let buttons = springboard.buttons.matching(identifier: name).allElementsBoundByIndex.filter { $0.isHittable }
            XCTAssertEqual(buttons.count, 1, "Expected one visible button with the exact identifier or label")
            try XCTUnwrap(buttons.first).tap()
        case "next-pages":
            let count = command.count ?? 1
            XCTAssertTrue((1...12).contains(count))
            for _ in 0..<count {
                try point(0.9, 0.78).press(forDuration: 0.05,
                    thenDragTo: point(0.1, 0.78), withVelocity: .fast, thenHoldForDuration: 0)
                Thread.sleep(forTimeInterval: 0.4)
            }
        case "remove-widget":
            let name = try XCTUnwrap(command.name)
            let menuRemove = springboard.buttons["Remove Widget"]
            if menuRemove.exists && menuRemove.isHittable {
                menuRemove.tap()
            } else {
                let widgets = springboard.icons.matching(identifier: name)
                    .allElementsBoundByIndex.filter { $0.isHittable && ($0.value as? String) == "Widget" }
                XCTAssertEqual(widgets.count, 1, "Expected one visible widget with this name")
                let widget = try XCTUnwrap(widgets.first)
                if !springboard.buttons["Done"].exists {
                    try point(0.5, 0.79).press(forDuration: 2)
                }
                let remove = widget.buttons["DeleteButton"]
                XCTAssertTrue(remove.waitForExistence(timeout: 5))
                remove.tap()
            }
            let alert = springboard.alerts.firstMatch
            XCTAssertTrue(alert.waitForExistence(timeout: 5))
            XCTAssertTrue(alert.label.contains(name), "Widget confirmation must name the requested provider")
            capture("remove-widget-confirmation")
            let confirm = alert.buttons["Remove"]
            XCTAssertTrue(confirm.exists)
            confirm.tap()
            XCTAssertTrue(alert.waitForNonExistence(timeout: 5))
            if springboard.buttons["Done"].exists { springboard.buttons["Done"].tap() }
            Thread.sleep(forTimeInterval: 1)
            XCTAssertFalse(springboard.icons.matching(identifier: name).allElementsBoundByIndex
                .contains { $0.isHittable && ($0.value as? String) == "Widget" })
        case "remove-icon":
            XCUIDevice.shared.press(.home)
            Thread.sleep(forTimeInterval: 0.5)
            XCUIDevice.shared.press(.home)
            Thread.sleep(forTimeInterval: 0.5)
            let page = command.page ?? 1
            XCTAssertTrue((1...12).contains(page))
            for _ in 1..<page {
                try point(0.9, 0.78).press(forDuration: 0.05,
                    thenDragTo: point(0.1, 0.78), withVelocity: .fast, thenHoldForDuration: 0)
                Thread.sleep(forTimeInterval: 0.4)
            }
            let name = try XCTUnwrap(command.name)
            let matches = springboard.icons.matching(identifier: name).allElementsBoundByIndex.filter { $0.isHittable }
            XCTAssertEqual(matches.count, 1, "Expected one visible icon matching the requested name")
            let icon = try XCTUnwrap(matches.first)
            try point(0.5, 0.79).press(forDuration: 2)
            let removeControl = icon.buttons["DeleteButton"]
            XCTAssertTrue(removeControl.waitForExistence(timeout: 5))
            removeControl.tap()
            let alert = springboard.alerts.firstMatch
            XCTAssertTrue(alert.waitForExistence(timeout: 5))
            XCTAssertTrue(alert.label.contains(name), "Removal dialog must name the requested app")
            let remove = alert.buttons["Remove from Home Screen"]
            XCTAssertTrue(remove.exists)
            capture("remove-icon-confirmation")
            remove.tap()
            XCTAssertTrue(alert.waitForNonExistence(timeout: 5))
            XCUIDevice.shared.press(.home)
            Thread.sleep(forTimeInterval: 1)
            XCTAssertFalse(springboard.icons.matching(identifier: name).allElementsBoundByIndex.contains { $0.isHittable },
                           "Icon still visible on this page")
        case "inspect": break
        case "home": XCUIDevice.shared.press(.home)
        case "tap": try point(command.x, command.y).tap()
        case "hold": try point(command.x, command.y).press(forDuration: command.duration ?? 1.2)
        case "type":
            let fields = (springboard.searchFields.allElementsBoundByIndex +
                springboard.textFields.matching(identifier: "SpotlightSearchField").allElementsBoundByIndex)
                .filter { $0.isHittable }
            XCTAssertEqual(fields.count, 1, "Expected one visible search field")
            let field = try XCTUnwrap(fields.first)
            field.tap()
            field.typeText(try XCTUnwrap(command.text))
        case "drag":
            try point(command.x, command.y).press(
                forDuration: command.duration ?? 1.2,
                thenDragTo: point(command.toX, command.toY),
                withVelocity: .slow,
                thenHoldForDuration: command.endHold ?? 0.5)
        default: XCTFail("Unsupported action: \(command.action)")
        }
        // Allow SpringBoard animations to settle before recording evidence.
        Thread.sleep(forTimeInterval: 1)
        let tree = springboard.debugDescription
        print("HSC_TREE_BEGIN\n\(tree)\nHSC_TREE_END")
        let attachment = XCTAttachment(string: tree)
        attachment.name = "springboard-tree"
        attachment.lifetime = .keepAlways
        add(attachment)
        capture("after")
    }
}
