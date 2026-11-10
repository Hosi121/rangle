import 'dart:async';

import 'package:flutter/material.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import '../viewer/free_view_adapter.dart';
import '../viewer/viewer_adapter.dart';
import '../viewer/playback_controller.dart';
import '../widgets/preview_viewer.dart';
import '../widgets/rangle_header.dart';
import '../widgets/viewer_modal.dart';
import '../widgets/viewer_viewport.dart';
import 'live_api.dart';
import 'live_controller.dart';

class LiveScreen extends StatefulWidget {
  const LiveScreen({
    this.api,
    this.initialId,
    this.viewerFactory,
    this.jobStatus,
    this.waitForSession = false,
    this.onBack,
    this.backLabel = '戻る',
    this.onSessionChanged,
    super.key,
  });
  final LiveApi? api;
  final String? initialId;
  final ViewerFactory? viewerFactory;
  final Widget? jobStatus;
  final bool waitForSession;
  final VoidCallback? onBack;
  final String backLabel;
  final ValueChanged<String?>? onSessionChanged;
  @override
  State<LiveScreen> createState() => _LiveScreenState();
}

class _LiveScreenState extends State<LiveScreen> with WidgetsBindingObserver {
  LiveController? _controller;
  final _playback = PlaybackController();
  LiveSegment? _displayed, _newest;
  DateTime? _sessionEpoch;
  final _known = <Uri, LiveSegment>{};
  Timer? _retry;
  int _sourceRevision = 0, _initialAttempt = 0;
  bool _visible = true, _replacementFailed = false, _gap = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _visible =
        lifecycle == null ||
        lifecycle == AppLifecycleState.resumed ||
        lifecycle == AppLifecycleState.inactive;
    if (widget.api != null &&
        (widget.initialId == null || liveId(widget.initialId!))) {
      _controller = LiveController(widget.api!, initialId: widget.initialId);
      _controller!.waitForSession(widget.waitForSession);
      _controller!.visible(_visible);
      _controller!.start();
    }
  }

  @override
  void didUpdateWidget(LiveScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    _controller?.waitForSession(widget.waitForSession);
  }

  void _select(String? id) {
    setState(() {
      _retry?.cancel();
      _displayed = _newest = null;
      _sessionEpoch = null;
      _known.clear();
      _replacementFailed = _gap = false;
      _sourceRevision = 0;
      _controller!.select(id);
    });
    widget.onSessionChanged?.call(id);
  }

  void _sourceReady(Uri source) {
    final segment = _known[source];
    if (segment == null) return;
    setState(() {
      _gap = _displayed != null && segment.start > _displayed!.end + .1;
      _displayed = segment;
      if (source == _newest?.scene) _replacementFailed = false;
    });
    if (source == _newest?.scene) _retry?.cancel();
  }

  void _sourceFailed({bool initial = false}) {
    if (!mounted) return;
    setState(() => _replacementFailed = true);
    _retry?.cancel();
    if (_visible) {
      _retry = Timer(const Duration(seconds: 1), () {
        if (mounted && _visible) {
          setState(() {
            if (initial && _displayed == null) {
              _initialAttempt++;
            } else {
              _sourceRevision++;
            }
          });
        }
      });
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Orbiting the iframe can blur Flutter without hiding the application.
    if (state == AppLifecycleState.inactive) return;
    _visible = state == AppLifecycleState.resumed;
    _controller?.visible(_visible);
    if (!_visible) {
      _retry?.cancel();
    } else if (_replacementFailed) {
      _sourceFailed(initial: _displayed == null);
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _retry?.cancel();
    _controller?.dispose();
    _playback.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ViewerViewport(
    child: Scaffold(
      backgroundColor: Colors.black,
      // Keep navigation outside the HTML viewer's bounds so it stays reachable
      // while the viewer is loading, failed, or handling pointer gestures.
      appBar: PreferredSize(
        preferredSize: const Size.fromHeight(64),
        child: PointerInterceptor(
          child: RangleHeader(
            title: 'LIVE',
            onBack: widget.onBack,
            backLabel: widget.backLabel,
            actions: [
              if (_controller?.selectedId != null)
                IconButton(
                  tooltip: 'LIVE一覧',
                  onPressed: () => _select(null),
                  icon: const Icon(Icons.list),
                ),
            ],
          ),
        ),
      ),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            Expanded(
              child: _controller == null
                  ? const ViewerMessage(
                      icon: Icons.cloud_off,
                      title: 'LIVEに接続できません',
                      description: '接続先または配信URLを確認してください。',
                    )
                  : ListenableBuilder(
                      listenable: _controller!,
                      builder: (context, _) => _content(),
                    ),
            ),
            if (widget.jobStatus != null)
              Material(
                color: const Color(0xFF191614),
                child: widget.jobStatus!,
              ),
          ],
        ),
      ),
    ),
  );

  Widget _content() {
    final controller = _controller!;
    if (controller.selectedId == null) {
      if (controller.sessions.isEmpty) {
        return ViewerMessage(
          icon: controller.reconnecting ? Icons.cloud_off : Icons.live_tv,
          title: controller.loading
              ? 'LIVEを確認しています'
              : controller.reconnecting
              ? 'LIVEに再接続しています'
              : '配信中のLIVEはありません',
          description: '配信が始まると、ここから視点を動かして見られます。',
          action: TextButton(
            onPressed: controller.refresh,
            child: const Text('再読み込み'),
          ),
        );
      }
      return ListView(
        padding: const EdgeInsets.fromLTRB(16, 20, 16, 24),
        children: [
          Text(
            '自由視点LIVE',
            style: Theme.of(context).textTheme.headlineSmall
                ?.copyWith(fontWeight: FontWeight.w800),
          ),
          const SizedBox(height: 8),
          const Text('見たい配信を選んでください。', style: TextStyle(color: Colors.white60)),
          const SizedBox(height: 24),
          if (controller.reconnecting) const ListTile(title: Text('再接続中')),
          for (final session in controller.sessions)
            Card(
              child: ListTile(
                contentPadding: const EdgeInsets.symmetric(
                  horizontal: 18,
                  vertical: 14,
                ),
                leading: const Icon(Icons.live_tv, color: Color(0xFFFF954A)),
                title: Text(session.title),
                subtitle: Text(_state(session.state)),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => _select(session.id),
              ),
            ),
        ],
      );
    }
    final session = controller.current;
    if (session == null) {
      final preparing =
          widget.waitForSession && (controller.loading || controller.missing);
      return ViewerMessage(
        icon: Icons.live_tv,
        title: preparing
            ? 'GPUと配信の準備をしています'
            : controller.missing
            ? 'LIVEが見つかりません'
            : controller.reconnecting
            ? 'LIVEに再接続しています'
            : '配信を確認しています',
        description: preparing
            ? 'クラウドの起動には数分かかることがあります。最初の映像を待っています。'
            : '前の画面に戻るか、接続を確認して再試行できます。',
        action: TextButton(
          onPressed: controller.refresh,
          child: const Text('再試行'),
        ),
      );
    }
    if (_sessionEpoch != session.startedAt) {
      _sessionEpoch = session.startedAt;
      _displayed = _newest = null;
      _known.clear();
      _retry?.cancel();
      _replacementFailed = _gap = false;
    }
    final offered = session.latest;
    if (offered != null &&
        (_newest == null || offered.sequence > _newest!.sequence)) {
      _newest = offered;
    }
    final latest = _newest;
    if (latest != null) {
      _known[latest.scene] = latest;
      while (_known.length > 64) {
        _known.remove(_known.keys.first);
      }
    }
    final reconnecting = controller.reconnecting || _replacementFailed;
    final label = controller.missing
        ? '配信を確認できません'
        : reconnecting
        ? '再接続中'
        : _state(session.state);
    final delay = _displayed == null
        ? null
        : DateTime.now()
                  .toUtc()
                  .difference(_displayed!.capturedAt)
                  .inMilliseconds
                  .clamp(0, 864000000) /
              1000;
    return Stack(
      fit: StackFit.expand,
      children: [
        if (latest != null)
          PreviewViewer(
            key: ValueKey(
              'live-${session.id}-${session.startedAt.microsecondsSinceEpoch}-$_initialAttempt',
            ),
            source: latest.scene,
            sha256: latest.sha256,
            sourceRevision: _sourceRevision,
            factory:
                widget.viewerFactory ??
                (source) => createLiveFreeViewAdapter(
                  source,
                  origins: widget.api!.origins.toList(),
                  sha256: _known[source]?.sha256,
                ),
            freeView: true,
            controller: _playback,
            showControls: false,
            onStatusChanged: (status) {
              if (status == ViewerStatus.error && _displayed == null) {
                _sourceFailed(initial: true);
              }
            },
            onSourceReady: _sourceReady,
            onSourceError: _sourceFailed,
          )
        else
          Column(
            children: [
              Padding(
                padding: const EdgeInsets.all(8),
                child: Text(
                  session.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Expanded(
                child: ViewerMessage(
                  icon: Icons.live_tv,
                  title: session.terminal
                      ? _state(session.state)
                      : '最初の映像を準備しています',
                  description: '映像の準備ができるまで、この画面でお待ちいただけます。',
                ),
              ),
            ],
          ),
        if (latest != null)
          Positioned(
            bottom: 64,
            left: 12,
            right: 12,
            child: PointerInterceptor(
              child: Material(
                color: Colors.black.withValues(alpha: .75),
                borderRadius: BorderRadius.circular(18),
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Row(
                        children: [
                          Expanded(
                            child: Text(
                              session.title,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                          IconButton(
                            tooltip: '配信の出典',
                            onPressed: () => showViewerDialog<void>(
                              context: context,
                              builder: (context) => AlertDialog(
                                title: const Text('配信の出典'),
                                content: SingleChildScrollView(
                                  child: SelectableText(
                                    '${session.attribution}\n${session.sourceUrl}\n${session.caveat}\n\n短い区間を順次更新して表示します。未取得の区間は補間しません。',
                                  ),
                                ),
                                actions: [
                                  TextButton(
                                    onPressed: () => Navigator.pop(context),
                                    child: const Text('閉じる'),
                                  ),
                                ],
                              ),
                            ),
                            icon: const Icon(Icons.info_outline),
                          ),
                        ],
                      ),
                      if (delay != null)
                        Text(
                          '撮影から約${delay.round()}秒',
                          style: Theme.of(context).textTheme.labelSmall,
                        ),
                      if (_displayed != null)
                        Text(
                          '${_displayed!.start.toStringAsFixed(1)}–${_displayed!.end.toStringAsFixed(1)}秒の区間${_gap ? '（区間に間隔があります）' : ''}',
                          style: Theme.of(context).textTheme.labelSmall,
                        ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        if (latest != null)
          Positioned(
            bottom: 8,
            left: 12,
            right: 12,
            child: PointerInterceptor(
              child: ListenableBuilder(
                listenable: _playback,
                builder: (context, _) {
                  final value = _playback.value;
                  return Row(
                    children: [
                      IconButton(
                        tooltip: value.playing ? '一時停止' : '再生',
                        onPressed:
                            value.status == ViewerStatus.ready && value.canPlay
                            ? () => unawaited(_playback.toggle())
                            : null,
                        icon: Icon(
                          value.playing ? Icons.pause : Icons.play_arrow,
                        ),
                      ),
                      const Spacer(),
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 12,
                          vertical: 8,
                        ),
                        decoration: BoxDecoration(
                          color: const Color(0xFFEC5800),
                          borderRadius: BorderRadius.circular(20),
                        ),
                        child: Text(
                          label,
                          style: const TextStyle(
                            fontSize: 11,
                            color: Colors.white,
                            fontWeight: FontWeight.w800,
                          ),
                        ),
                      ),
                    ],
                  );
                },
              ),
            ),
          ),
      ],
    );
  }
}

String _state(String state) => switch (state) {
  'live' => 'LIVE',
  'ended' => '配信終了',
  'failed' => '配信に失敗しました',
  _ => '配信準備中',
};
