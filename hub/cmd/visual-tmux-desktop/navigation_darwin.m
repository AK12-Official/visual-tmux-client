//go:build desktop && darwin

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

@interface VTCNavigationGuard : NSObject <WKNavigationDelegate, WKUIDelegate>
@property(nonatomic, copy) NSString *origin;
@property(nonatomic, weak) id<WKNavigationDelegate> previous;
@property(nonatomic, weak) id<WKUIDelegate> previousUI;
@property(nonatomic) int blocked;
@property(nonatomic, strong) NSNumber *probeReply;
@property(nonatomic, copy) NSString *probeText;
@end

@implementation VTCNavigationGuard
- (BOOL)owns:(NSURL *)url {
    if ([url.absoluteString isEqualToString:@"about:blank"]) return YES;
    NSURL *owned = [NSURL URLWithString:self.origin];
    return [url.scheme isEqualToString:owned.scheme] && [url.host isEqualToString:owned.host] &&
        [url.port isEqualToNumber:owned.port] && !url.user && !url.password;
}
- (BOOL)respondsToSelector:(SEL)selector {
    return [super respondsToSelector:selector] || [self.previous respondsToSelector:selector] ||
        [self.previousUI respondsToSelector:selector];
}
- (id)forwardingTargetForSelector:(SEL)selector {
    if ([self.previous respondsToSelector:selector]) return self.previous;
    return self.previousUI;
}
- (void)webView:(WKWebView *)view decidePolicyForNavigationAction:(WKNavigationAction *)action
        decisionHandler:(void (^)(WKNavigationActionPolicy))handler {
    if (![self owns:action.request.URL]) { self.blocked++; handler(WKNavigationActionPolicyCancel); return; }
    if ([self.previous respondsToSelector:_cmd]) {
        [self.previous webView:view decidePolicyForNavigationAction:action decisionHandler:handler];
    } else { handler(WKNavigationActionPolicyAllow); }
}
- (void)webView:(WKWebView *)view decidePolicyForNavigationResponse:(WKNavigationResponse *)response
        decisionHandler:(void (^)(WKNavigationResponsePolicy))handler {
    if (![self owns:response.response.URL]) { self.blocked++; handler(WKNavigationResponsePolicyCancel); return; }
    if ([self.previous respondsToSelector:_cmd]) {
        [self.previous webView:view decidePolicyForNavigationResponse:response decisionHandler:handler];
    } else { handler(WKNavigationResponsePolicyAllow); }
}
- (NSAlert *)dialog:(NSString *)message {
    NSAlert *alert = [NSAlert new];
    alert.messageText = @"Visual Tmux Client";
    alert.informativeText = message ?: @"";
    [alert addButtonWithTitle:@"OK / 确定"];
    return alert;
}
- (BOOL)ownedFrame:(WKFrameInfo *)frame {
    return frame.isMainFrame && [self owns:frame.request.URL];
}
- (void)respondToProbe:(NSAlert *)alert input:(NSTextField *)input {
    NSNumber *reply = self.probeReply;
    if (!reply) return;
    if (input) input.stringValue = self.probeText ?: @"";
    self.probeReply = nil;
    self.probeText = nil;
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 200 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
        NSUInteger index = reply.boolValue ? 0 : alert.buttons.count - 1;
        [alert.buttons[index] performClick:nil];
    });
}
- (void)webView:(WKWebView *)view runJavaScriptConfirmPanelWithMessage:(NSString *)message
        initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(BOOL))completion {
    if (![self ownedFrame:frame]) { completion(NO); return; }
    NSAlert *alert = [self dialog:message];
    [alert addButtonWithTitle:@"Cancel / 取消"];
    alert.buttons[0].keyEquivalent = @"";
    alert.buttons[1].keyEquivalent = @"\r";
    [alert beginSheetModalForWindow:view.window completionHandler:^(NSModalResponse response) {
        completion(response == NSAlertFirstButtonReturn);
    }];
    [self respondToProbe:alert input:nil];
}
- (void)webView:(WKWebView *)view runJavaScriptAlertPanelWithMessage:(NSString *)message
        initiatedByFrame:(WKFrameInfo *)frame completionHandler:(void (^)(void))completion {
    if (![self ownedFrame:frame]) { completion(); return; }
    [[self dialog:message] beginSheetModalForWindow:view.window completionHandler:^(NSModalResponse response) {
        completion();
    }];
}
- (void)webView:(WKWebView *)view runJavaScriptTextInputPanelWithPrompt:(NSString *)prompt
        defaultText:(NSString *)defaultText initiatedByFrame:(WKFrameInfo *)frame
        completionHandler:(void (^)(NSString *))completion {
    if (![self ownedFrame:frame]) { completion(nil); return; }
    NSAlert *alert = [self dialog:prompt];
    [alert addButtonWithTitle:@"Cancel / 取消"];
    NSTextField *input = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 360, 24)];
    input.stringValue = defaultText ?: @"";
    alert.accessoryView = input;
    alert.window.initialFirstResponder = input;
    [alert beginSheetModalForWindow:view.window completionHandler:^(NSModalResponse response) {
        completion(response == NSAlertFirstButtonReturn ? input.stringValue : nil);
    }];
    [self respondToProbe:alert input:input];
}
@end

static const char VTCGuardKey;
static WKWebView *vtcFindWebView(NSView *view) {
    if ([view isKindOfClass:[WKWebView class]]) return (WKWebView *)view;
    for (NSView *child in view.subviews) {
        WKWebView *found = vtcFindWebView(child);
        if (found) return found;
    }
    return nil;
}

int vtcInstallNavigation(void *window, const char *origin) {
    WKWebView *view = vtcFindWebView(((__bridge NSWindow *)window).contentView);
    if (!view) return 0;
    if (!objc_getAssociatedObject(view, &VTCGuardKey)) {
        VTCNavigationGuard *guard = [VTCNavigationGuard new];
        guard.origin = [NSString stringWithUTF8String:origin];
        guard.previous = view.navigationDelegate;
        guard.previousUI = view.UIDelegate;
        objc_setAssociatedObject(view, &VTCGuardKey, guard, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
        view.navigationDelegate = guard;
        view.UIDelegate = guard;
    }
    return 1;
}

int vtcBlockedCount(void *window) {
    WKWebView *view = vtcFindWebView(((__bridge NSWindow *)window).contentView);
    VTCNavigationGuard *guard = objc_getAssociatedObject(view, &VTCGuardKey);
    return guard ? guard.blocked : -1;
}

int vtcScheduleDialogProbe(void *window, int approve, const char *text) {
    WKWebView *view = vtcFindWebView(((__bridge NSWindow *)window).contentView);
    VTCNavigationGuard *guard = objc_getAssociatedObject(view, &VTCGuardKey);
    if (!guard) return 0;
    guard.probeReply = @(approve != 0);
    guard.probeText = [NSString stringWithUTF8String:text];
    return 1;
}

static BOOL vtcPressSaveButton(id element, NSString *prompt, NSUInteger depth, NSMutableSet *visited) {
    if (!element || depth > 24 || visited.count > 2000) return NO;
    NSValue *identity = [NSValue valueWithNonretainedObject:element];
    if ([visited containsObject:identity]) return NO;
    [visited addObject:identity];
    if ([element respondsToSelector:@selector(accessibilityRole)] &&
        [[element accessibilityRole] isEqualToString:NSAccessibilityButtonRole] &&
        [element respondsToSelector:@selector(accessibilityLabel)] &&
        [[element accessibilityLabel] isEqualToString:prompt] &&
        [element respondsToSelector:@selector(accessibilityPerformPress)]) {
        return [element accessibilityPerformPress];
    }
    if ([element respondsToSelector:@selector(accessibilityChildren)]) {
        for (id child in [element accessibilityChildren]) {
            if (vtcPressSaveButton(child, prompt, depth + 1, visited)) return YES;
        }
    }
    return NO;
}

int vtcScheduleSaveProbe(void *window, int approve, const char *path) {
    NSWindow *parent = (__bridge NSWindow *)window;
    NSString *destination = [NSString stringWithUTF8String:path];
    __block int attempts = 0;
    NSTimer *timer = [NSTimer timerWithTimeInterval:0.1 repeats:YES block:^(NSTimer *timer) {
        NSWindow *sheet = parent.attachedSheet;
        if (![sheet isKindOfClass:[NSSavePanel class]]) sheet = NSApp.modalWindow;
        if ([sheet isKindOfClass:[NSSavePanel class]]) {
            NSSavePanel *panel = (NSSavePanel *)sheet;
            if (approve) {
                panel.directoryURL = [NSURL fileURLWithPath:destination.stringByDeletingLastPathComponent];
                panel.nameFieldStringValue = destination.lastPathComponent;
                dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 500 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
                    // Use the panel's own accessibility tree, including its
                    // remote content. No global input or foreign app is touched.
                    BOOL pressed = vtcPressSaveButton(panel, panel.prompt, 0, [NSMutableSet new]);
                    fprintf(stderr, "spike: owned save accessibility button pressed: %s\n", pressed ? "yes" : "no");
                });
            } else { [panel cancel:nil]; }
            [timer invalidate];
        } else if (++attempts >= 50) { [timer invalidate]; }
    }];
    [[NSRunLoop mainRunLoop] addTimer:timer forMode:NSRunLoopCommonModes];
    return 1;
}

void vtcCancelOwnedDialogs(void *window) {
    NSWindow *parent = (__bridge NSWindow *)window;
    NSWindow *sheet = parent.attachedSheet;
    if (!sheet) return;
    if ([sheet isKindOfClass:[NSSavePanel class]]) {
        [(NSSavePanel *)sheet cancel:nil];
    } else {
        [parent endSheet:sheet returnCode:NSModalResponseCancel];
    }
}
