// Objective-C half of the purgeable reading. Compiled only when cgo is
// enabled; a CGO_ENABLED=0 build ignores this file entirely.
#import <Foundation/Foundation.h>
#include <stdio.h>

int storix_important_usage(const char *path, long long *out, char *errbuf, int errlen);

// storix_important_usage writes NSURLVolumeAvailableCapacityForImportantUsageKey
// for the volume containing path into *out. Returns 0 on success, -1 on
// failure with a NUL-terminated message in errbuf.
int storix_important_usage(const char *path, long long *out, char *errbuf, int errlen) {
	@autoreleasepool {
		NSString *p = [NSString stringWithUTF8String:path];
		if (p == nil) {
			snprintf(errbuf, errlen, "path is not valid UTF-8");
			return -1;
		}
		NSURL *url = [NSURL fileURLWithPath:p isDirectory:YES];
		NSError *err = nil;
		NSNumber *value = nil;
		if (![url getResourceValue:&value
		                    forKey:NSURLVolumeAvailableCapacityForImportantUsageKey
		                     error:&err]) {
			const char *msg = "unknown error";
			if (err != nil) {
				msg = [[err localizedDescription] UTF8String];
			}
			snprintf(errbuf, errlen, "%s", msg);
			return -1;
		}
		if (value == nil) {
			snprintf(errbuf, errlen, "volume reports no important-usage capacity");
			return -1;
		}
		*out = (long long)[value longLongValue];
		return 0;
	}
}
