import 'package:flutter/foundation.dart';

/// Focused media time and explicit play intent survive replacement of a player.
@immutable
class PlaybackState {
  const PlaybackState(this.position, this.playing);
  final Duration position;
  final bool playing;
}

@immutable
class PreviewSceneMapping {
  const PreviewSceneMapping({
    this.previewStart = Duration.zero,
    this.sceneStart = Duration.zero,
  });
  final Duration previewStart, sceneStart;
  Duration toScene(Duration preview) => preview - previewStart + sceneStart;
  Duration toPreview(Duration scene) => scene - sceneStart + previewStart;
  Map<String, Object> toJson() => {
    'previewStartSeconds': previewStart.inMicroseconds / 1000000,
    'sceneStartSeconds': sceneStart.inMicroseconds / 1000000,
  };
}

@immutable
class ViewerTimeline {
  const ViewerTimeline({
    required this.frameCount,
    required this.frameRate,
    this.mapping,
  });
  final int frameCount;
  final double frameRate;
  final PreviewSceneMapping? mapping;
  int frameAt(Duration position) =>
      (position.inMicroseconds * frameRate / 1000000).round().clamp(
        0,
        frameCount - 1,
      );
  Duration atFrame(int frame) => Duration(
    microseconds: (frame.clamp(0, frameCount - 1) * 1000000 / frameRate)
        .round(),
  );
  Duration sample(Duration position) => atFrame(frameAt(position));
}
