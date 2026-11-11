//go:build desktop && darwin

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

@interface VTCNavigationGuard : NSObject <WKNavigationDelegate>
@property(nonatomic, copy) NSString *origin;
@property(nonatomic, weak) id<WKNavigationDelegate> previous;
@property(nonatomic) int blocked;
@end

@implementation VTCNavigationGuard
- (BOOL)owns:(NSURL *)url {
    if ([url.absoluteString isEqualToString:@"about:blank"]) return YES;
    NSURL *owned = [NSURL URLWithString:self.origin];
    return [url.scheme isEqualToString:owned.scheme] && [url.host isEqualToString:owned.host] &&
        [url.port isEqualToNumber:owned.port] && !url.user && !url.password;
}
- (BOOL)respondsToSelector:(SEL)selector {
    return [super respondsToSelector:selector] || [self.previous respondsToSelector:selector];
}
- (id)forwardingTargetForSelector:(SEL)selector { return self.previous; }
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
        objc_setAssociatedObject(view, &VTCGuardKey, guard, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
        view.navigationDelegate = guard;
    }
    return 1;
}

int vtcBlockedCount(void *window) {
    WKWebView *view = vtcFindWebView(((__bridge NSWindow *)window).contentView);
    VTCNavigationGuard *guard = objc_getAssociatedObject(view, &VTCGuardKey);
    return guard ? guard.blocked : -1;
}
