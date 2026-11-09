import 'dart:async';

import 'package:flutter/material.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import '../viewer/free_view_adapter.dart';
import '../viewer/viewer_adapter.dart';
import '../widgets/preview_viewer.dart';
import 'live_api.dart';
import 'live_controller.dart';

class LiveScreen extends StatefulWidget {
  const LiveScreen({this.api, this.initialId, this.viewerFactory, super.key});
  final LiveApi? api;
  final String? initialId;
  final ViewerFactory? viewerFactory;
  @override
  State<LiveScreen> createState() => _LiveScreenState();
}

class _LiveScreenState extends State<LiveScreen> with WidgetsBindingObserver {
  LiveController? _controller;
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
      _controller!.visible(_visible);
      _controller!.start();
    }
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
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text('LIVE'),
      actions: [
        if (_controller?.selectedId != null)
          IconButton(
            tooltip: 'LIVE一覧',
            onPressed: () => _select(null),
            icon: const Icon(Icons.list),
          ),
      ],
    ),
    body: SafeArea(
      top: false,
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
        children: [
          if (controller.reconnecting) const ListTile(title: Text('再接続中')),
          for (final session in controller.sessions)
            ListTile(
              leading: const Icon(Icons.live_tv),
              title: Text(session.title),
              subtitle: Text(_state(session.state)),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => _select(session.id),
            ),
        ],
      );
    }
    final session = controller.current;
    if (session == null) {
      return ViewerMessage(
        icon: Icons.live_tv,
        title: controller.missing
            ? 'LIVEが見つかりません'
            : controller.reconnecting
            ? 'LIVEに再接続しています'
            : '配信を確認しています',
        description: '前の画面に戻るか、接続を確認して再試行できます。',
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
            onStatusChanged: (status) {
              if (status == ViewerStatus.error && _displayed == null) {
                _sourceFailed(initial: true);
              }
            },
            onSourceReady: _sourceReady,
            onSourceError: _sourceFailed,
          )
        else
          ViewerMessage(
            icon: Icons.live_tv,
            title: session.terminal ? _state(session.state) : '最初の映像を準備しています',
            description: '映像の準備ができるまで、この画面でお待ちいただけます。',
          ),
        Positioned(
          top: 8,
          left: 12,
          right: 12,
          child: PointerInterceptor(
            child: Material(
              color: Colors.black.withValues(alpha: .75),
              borderRadius: BorderRadius.circular(12),
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
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                        IconButton(
                          tooltip: '配信の出典',
                          onPressed: () => showDialog<void>(
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
                    Text(
                      '$label${delay == null ? '' : ' · 撮影から約${delay.round()}秒'}',
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
