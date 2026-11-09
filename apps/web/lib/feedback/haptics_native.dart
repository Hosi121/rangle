import 'dart:async';

import 'package:flutter/services.dart';

void pulse(int milliseconds) {
  unawaited(HapticFeedback.lightImpact().catchError((Object _) {}));
}
