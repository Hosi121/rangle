import 'dart:async';

import 'package:flutter/material.dart';

import '../community/account_controller.dart';
import '../community/account_dialog.dart';
import '../community/community_api.dart';
import '../live/live_api.dart';
import '../publishers/publisher_controller.dart';
import '../live/live_screen.dart';
import '../viewer/viewer_adapter.dart';
import '../widgets/viewer_modal.dart';
import '../widgets/rangle_header.dart';
import '../widgets/preview_viewer.dart';
import 'generation_api.dart';
import 'generation_progress.dart';

class GenerationScreen extends StatefulWidget {
  const GenerationScreen({
    required this.account,
    required this.onOpenResult,
    this.onPublicationChanged,
    this.api,
    this.live,
    this.liveFactory,
    this.liveViewer = false,
    this.initialLiveId,
    this.managementHome = false,
    this.publisher,
    this.onBack,
    this.onManagePublishers,
    this.headerActions = const [],
    super.key,
  }) : assert(account == null || publisher == null);
  final AccountController? account;
  final PublisherController? publisher;
  final VoidCallback? onBack, onManagePublishers;
  final List<Widget> headerActions;
  final GenerationApi? api;
  final LiveApi? live;
  final ViewerFactory? liveFactory;
  final bool liveViewer;
  final bool managementHome;
  final String? initialLiveId;
  final ValueChanged<Uri> onOpenResult;
  final VoidCallback? onPublicationChanged;

  @override
  State<GenerationScreen> createState() => _GenerationScreenState();
}

class _GenerationScreenState extends State<GenerationScreen>
    with WidgetsBindingObserver {
  late final GenerationApi _api;
  GenerationCatalog? _catalog;
  List<GenerationJob> _jobs = [];
  String? _owner,
      _preset,
      _requestId,
      _requestPreset,
      _error,
      _submitError,
      _busyJob;
  final _jobErrors = <String, String>{};
  bool _loading = true, _submitting = false, _accepted = false;
  bool _foreground = true, _covered = false;
  bool _lastAccess = false;
  late bool _showLive;
  String? _liveId;
  int _epoch = 0;
  Timer? _poll;

  bool get _visible => _foreground && !_covered;
  bool get _signedIn => widget.publisher != null
      ? widget.publisher!.identity != null
      : widget.account?.user != null;
  bool get _canGenerate => widget.publisher != null
      ? widget.publisher!.identity != null
      : widget.account?.user?.isAdmin == true;
  String? get _currentOwner {
    if (widget.publisher != null) {
      final identity = widget.publisher!.identity;
      return identity == null
          ? null
          : 'publisher:${identity.account.id}:${identity.staff.id}';
    }
    return widget.account?.user?.id;
  }

  bool get _active => _jobs.any((job) => job.active);
  bool get _busy => _submitting || _busyJob != null;

  @override
  void initState() {
    super.initState();
    _showLive = widget.liveViewer;
    _liveId = widget.initialLiveId;
    _api =
        widget.api ??
        GenerationApi(
          endpoint:
              widget.publisher?.api.endpoint ??
              widget.account?.api.endpoint ??
              Uri.base.resolve('/api/v1/'),
          publisherAccountId: widget.publisher?.identity?.account.id,
        );
    _owner = _currentOwner;
    if (widget.publisher != null &&
        _api.publisherAccountId != widget.publisher!.identity?.account.id) {
      throw StateError(
        'Generation client must use the signed-in publisher scope.',
      );
    }
    _lastAccess = _canGenerate;
    widget.account?.addListener(_accountChanged);
    widget.publisher?.addListener(_accountChanged);
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _foreground =
        lifecycle == null ||
        lifecycle == AppLifecycleState.resumed ||
        (_showLive && lifecycle == AppLifecycleState.inactive);
    if (_foreground) unawaited(_load());
  }

  void _accountChanged() {
    if (!mounted) return;
    final owner = _currentOwner;
    if (owner != _owner || _canGenerate != _lastAccess) {
      _owner = owner;
      _lastAccess = _canGenerate;
      _pause();
      setState(() {
        _jobs = [];
        _requestId = _requestPreset = _submitError = _busyJob = null;
        _jobErrors.clear();
        _accepted = _submitting = false;
      });
      if (_visible) unawaited(_load());
    } else {
      setState(() {});
    }
  }

  void _pause() {
    _epoch++;
    _poll?.cancel();
    _poll = null;
    _api.cancelPending();
  }

  void _schedule([Duration delay = const Duration(seconds: 4)]) {
    _poll?.cancel();
    if (!_visible || !_signedIn || !_active || !mounted) return;
    _poll = Timer(delay, _refreshActive);
  }

  Future<void> _load() async {
    _pause();
    final epoch = _epoch;
    setState(() {
      _loading = true;
      _submitting = false;
      _busyJob = null;
      _catalog = null;
      _error = null;
    });
    try {
      if (!_canGenerate) {
        _jobs = [];
        return;
      }
      if (!_showLive) {
        final catalog = await _api.presets();
        if (!mounted || epoch != _epoch) return;
        _catalog = catalog;
        if (!catalog.available ||
            (catalog.presets.isEmpty && widget.publisher == null)) {
          throw const GenerationFailure('generation_unavailable');
        }
        if (!catalog.presets.any((preset) => preset.id == _preset)) {
          _preset = catalog.presets.firstOrNull?.id;
        }
      }
      if (_signedIn) {
        final jobs = await _api.jobs();
        if (!mounted || epoch != _epoch) return;
        _jobs = jobs..sort((a, b) => b.createdAt.compareTo(a.createdAt));
      }
    } catch (error) {
      if (mounted && epoch == _epoch && _visible) _error = _message(error);
    } finally {
      if (mounted && epoch == _epoch) {
        setState(() => _loading = false);
        _schedule();
      }
    }
  }

  String _message(Object error) {
    if (error is GenerationFailure) {
      if (error.code == 'not_authenticated') {
        if (widget.publisher != null) {
          widget.publisher!.expired();
        } else {
          widget.account?.expired();
        }
      }
      return error.message;
    }
    return const GenerationFailure('network_error').message;
  }

  void _updateJob(GenerationJob job) {
    _jobs.removeWhere((item) => item.id == job.id);
    _jobs.add(job);
    _jobs.sort((a, b) => b.createdAt.compareTo(a.createdAt));
  }

  Future<void> _refreshActive() async {
    if (!_visible || !_signedIn || !_active || _busy) {
      _schedule();
      return;
    }
    final epoch = _epoch;
    var delay = const Duration(seconds: 4);
    final id = _jobs.firstWhere((job) => job.active).id;
    try {
      final job = await _api.get(id);
      if (!mounted || epoch != _epoch || !_visible) return;
      setState(() {
        _updateJob(job);
        _error = null;
      });
    } catch (error) {
      if (!mounted || epoch != _epoch || !_visible) return;
      setState(() => _error = _message(error));
      delay =
          error is GenerationFailure &&
              error.retryAfter != null &&
              error.retryAfter! > const Duration(seconds: 4)
          ? error.retryAfter!
          : const Duration(seconds: 8);
    } finally {
      if (mounted && epoch == _epoch) _schedule(delay);
    }
  }

  Future<void> _signIn() async {
    if (_covered) return;
    _covered = true;
    _pause();
    await showAccountDialog(context, widget.account);
    if (!mounted) return;
    _covered = false;
    if (_foreground) await _load();
  }

  Future<void> _submit() async {
    if (_busy || _active || !_signedIn || _preset == null) return;
    if (_accepted) {
      _requestId = _requestPreset = null;
      _accepted = false;
    }
    _requestId ??= CommunityApi.requestId();
    _requestPreset ??= _preset;
    final epoch = _epoch;
    _poll?.cancel();
    setState(() {
      _submitting = true;
      _submitError = null;
    });
    try {
      final job = await _api.create(_requestPreset!, _requestId!);
      if (!mounted || epoch != _epoch) return;
      setState(() {
        _accepted = true;
        _updateJob(job);
      });
    } catch (error) {
      if (mounted && epoch == _epoch && _visible) {
        setState(() => _submitError = _message(error));
      }
    } finally {
      if (mounted && epoch == _epoch) {
        setState(() => _submitting = false);
        _schedule();
      }
    }
  }

  Future<void> _cancel(GenerationJob job) async {
    if (_busy || _loading || _covered) return;
    _covered = true;
    _pause();
    final confirmed = await showViewerDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(job.isLive ? 'LIVEの生成を停止しますか？' : '生成を中止しますか？'),
        content: Text(
          job.isLive
              ? '新しい映像の生成を停止します。公開済みの区間は残る場合があります。'
              : '途中までの生成結果は表示されません。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('続ける'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('中止する'),
          ),
        ],
      ),
    );
    if (!mounted) return;
    _covered = false;
    if (confirmed != true) {
      _schedule();
      return;
    }
    await _jobAction(job, () => _api.cancel(job.id));
  }

  Future<void> _open(GenerationJob job) => _jobAction(job, () async {
    final result = job.viewerUrl == null ? await _api.get(job.id) : job;
    if (result.viewerUrl == null) throw const GenerationFailure('not_found');
    return result;
  }, open: true);

  Future<void> _setPublished(GenerationJob job) => _jobAction(
    job,
    () => _api.setPublished(job.id, published: !job.published),
    onSuccess: widget.onPublicationChanged,
  );

  Future<void> _jobAction(
    GenerationJob job,
    Future<GenerationJob> Function() action, {
    bool open = false,
    VoidCallback? onSuccess,
  }) async {
    if (_busy || _loading || !_signedIn) return;
    final epoch = _epoch;
    _poll?.cancel();
    setState(() {
      _busyJob = job.id;
      _jobErrors.remove(job.id);
    });
    try {
      final result = await action();
      if (!mounted || epoch != _epoch) return;
      setState(() => _updateJob(result));
      onSuccess?.call();
      if (open && _visible) {
        if (result.isLive) {
          setState(() {
            _liveId = result.id;
            _showLive = true;
          });
        } else {
          widget.onOpenResult(result.viewerUrl!);
        }
      }
    } catch (error) {
      if (mounted && epoch == _epoch && _visible) {
        setState(() => _jobErrors[job.id] = _message(error));
      }
    } finally {
      if (mounted && epoch == _epoch) {
        setState(() => _busyJob = null);
        _schedule();
      }
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Focusing the GS iframe does not hide the visible job controls.
    if (_showLive && state == AppLifecycleState.inactive) return;
    _foreground = state == AppLifecycleState.resumed;
    if (_foreground && !_covered) {
      unawaited(_load());
    } else {
      _pause();
    }
  }

  @override
  void dispose() {
    _pause();
    if (widget.api == null) _api.close();
    widget.account?.removeListener(_accountChanged);
    widget.publisher?.removeListener(_accountChanged);
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  Widget _errorText(String message) => Padding(
    padding: const EdgeInsets.only(top: 8),
    child: Semantics(
      liveRegion: true,
      child: Text(
        message,
        style: TextStyle(color: Theme.of(context).colorScheme.error),
      ),
    ),
  );

  Widget? _liveStatus(GenerationJob? job) {
    if (job == null && _error == null) return null;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (job != null)
            Semantics(liveRegion: true, child: Text(job.stateLabel)),
          if (job?.active == true && job!.state != 'cancelling')
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton(
                onPressed: _busy || _loading ? null : () => _cancel(job),
                child: const Text('LIVEを停止'),
              ),
            ),
          if (_error != null) _errorText(_error!),
          if (job == null)
            TextButton(
              onPressed: _loading ? null : _load,
              child: const Text('ジョブ状態を再読み込み'),
            ),
          if (_jobErrors[job?.id] != null) _errorText(_jobErrors[job?.id]!),
        ],
      ),
    );
  }

  Widget _liveView() {
    final job = _jobs
        .where((job) => job.isLive && job.id == _liveId)
        .firstOrNull;
    return LiveScreen(
      api: widget.live,
      viewerFactory: widget.liveFactory,
      initialId: _liveId,
      waitForSession: job?.active == true,
      onSessionChanged: (id) => setState(() => _liveId = id),
      backLabel: widget.liveViewer ? 'フィードに戻る' : '生成に戻る',
      onBack: widget.liveViewer
          ? () => Navigator.pop(context)
          : () {
              setState(() => _showLive = false);
              unawaited(_load());
            },
      jobStatus: _liveStatus(job),
    );
  }

  bool get _livePreset =>
      _catalog?.presets
          .where((item) => item.id == _preset)
          .firstOrNull
          ?.isLive ==
      true;

  @override
  Widget build(BuildContext context) =>
      _showLive ? _liveView() : _generationForm(context);

  Widget _generationForm(BuildContext context) => Scaffold(
    appBar: RangleHeader(
      title: widget.publisher != null
          ? '団体管理'
          : widget.managementHome
          ? '管理'
          : 'GS生成',
      onBack: widget.onBack,
      actions: [
        ...widget.headerActions,
        if (widget.managementHome && widget.onManagePublishers != null)
          IconButton(
            tooltip: '団体管理',
            onPressed: widget.onManagePublishers,
            icon: const Icon(Icons.business_outlined),
          ),
        if (widget.managementHome)
          IconButton(
            tooltip: 'アカウント',
            onPressed: () => showAccountDialog(context, widget.account),
            icon: const Icon(Icons.account_circle_outlined),
          ),
        IconButton(
          tooltip: '状態を再読み込み',
          onPressed: _loading || _busy ? null : _load,
          icon: const Icon(Icons.refresh),
        ),
      ],
    ),
    body: !_canGenerate
        ? SafeArea(
            child: ViewerMessage(
              icon: Icons.admin_panel_settings_outlined,
              title: _signedIn ? '管理者専用のページです' : '管理者ログイン',
              description: 'GS生成とLIVE配信の管理は管理者アカウントで利用できます。',
              action: FilledButton(
                onPressed: widget.account == null || widget.account!.loading
                    ? null
                    : _signIn,
                child: Text(_signedIn ? 'アカウントを開く' : 'ログイン'),
              ),
            ),
          )
        : SafeArea(
            child: Center(
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 600),
                child: ListView(
                  padding: const EdgeInsets.all(20),
                  children: [
                    Text(
                      widget.publisher?.identity?.account.name ??
                          'スポーツの一瞬と動きを3Dに',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                    const SizedBox(height: 8),
                    if (widget.publisher?.identity case final identity?)
                      Text(
                        '${identity.staff.username} · ${identity.staff.roleLabel}',
                      )
                    else
                      const Text('確認済みの複数視点データから、自由視点で見返せるシーンを生成します。'),
                    const SizedBox(height: 24),
                    if (_loading) const Text('生成の受付状況を確認しています'),
                    if (_error != null) ...[
                      _errorText(_error!),
                      Align(
                        alignment: Alignment.centerLeft,
                        child: OutlinedButton(
                          onPressed: _loading || _busy ? null : _load,
                          child: const Text('再読み込み'),
                        ),
                      ),
                    ],
                    if (_catalog?.available == true &&
                        _catalog!.presets.isEmpty)
                      const Text('利用できるデータがありません。'),
                    if (_catalog?.available == true &&
                        _catalog!.presets.isNotEmpty) ...[
                      DropdownButtonFormField<String>(
                        key: ValueKey(_preset),
                        initialValue: _preset,
                        isExpanded: true,
                        decoration: const InputDecoration(
                          labelText: '生成に使うデータ',
                        ),
                        items: _catalog!.presets
                            .map(
                              (preset) => DropdownMenuItem(
                                value: preset.id,
                                child: Text(
                                  preset.title,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                            )
                            .toList(),
                        onChanged:
                            _busy ||
                                _active ||
                                (_requestId != null && !_accepted)
                            ? null
                            : (value) => setState(() => _preset = value),
                      ),
                      if (_preset != null) ...[
                        const SizedBox(height: 12),
                        for (final preset in _catalog!.presets.where(
                          (item) => item.id == _preset,
                        )) ...[
                          Text(preset.formatLabel),
                          const SizedBox(height: 4),
                          Text(preset.sport),
                          const SizedBox(height: 4),
                          Text(preset.description),
                          const SizedBox(height: 8),
                          Text(
                            '出典：${preset.source}\n${preset.license}',
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ],
                      ],
                      const SizedBox(height: 20),
                      if (!_signedIn) ...[
                        const Text('生成するにはログインしてください。'),
                        const SizedBox(height: 8),
                        FilledButton(
                          onPressed:
                              widget.account == null || widget.account!.loading
                              ? null
                              : _signIn,
                          child: const Text('ログインして生成する'),
                        ),
                      ] else ...[
                        FilledButton(
                          onPressed: _loading || _busy || _active
                              ? null
                              : _submit,
                          child: Text(
                            _submitting
                                ? '依頼を送信しています'
                                : _requestId != null && !_accepted
                                ? '同じ依頼で再試行'
                                : _livePreset
                                ? 'LIVEを開始'
                                : '生成を開始',
                          ),
                        ),
                        if (_submitError != null) _errorText(_submitError!),
                        if (_active)
                          const Padding(
                            padding: EdgeInsets.only(top: 8),
                            child: Text('画面を閉じても生成は続きます。状態はこの画面で確認できます。'),
                          ),
                      ],
                    ],
                    if (_signedIn) ...[
                      const SizedBox(height: 28),
                      Text(
                        widget.publisher == null ? '自分の生成' : '団体の生成',
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      const SizedBox(height: 8),
                      if (!_loading && _jobs.isEmpty)
                        const Text('まだ生成の依頼はありません。'),
                      for (final job in _jobs.take(10))
                        Card(
                          child: Padding(
                            padding: const EdgeInsets.all(16),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.stretch,
                              children: [
                                Text(
                                  _catalog?.presets
                                          .where(
                                            (preset) => preset.id == job.preset,
                                          )
                                          .firstOrNull
                                          ?.title ??
                                      'GSの生成',
                                  style: Theme.of(context).textTheme.titleSmall,
                                ),
                                const SizedBox(height: 4),
                                Semantics(
                                  liveRegion: true,
                                  child: Text(job.stateLabel),
                                ),
                                GenerationProgressIndicator(
                                  job: job,
                                  unavailable: _error != null,
                                ),
                                if (job.active && job.state != 'cancelling')
                                  Align(
                                    alignment: Alignment.centerLeft,
                                    child: TextButton(
                                      onPressed: _busy || _loading
                                          ? null
                                          : () => _cancel(job),
                                      child: Text(
                                        job.isLive ? 'LIVEを停止' : '生成を中止',
                                      ),
                                    ),
                                  ),
                                if (job.isLive)
                                  FilledButton.tonal(
                                    onPressed: _busy || _loading
                                        ? null
                                        : () => _open(job),
                                    child: const Text('LIVEを見る'),
                                  ),
                                if (!job.isLive &&
                                    job.state == 'succeeded') ...[
                                  if (job.published)
                                    const Padding(
                                      padding: EdgeInsets.symmetric(
                                        vertical: 8,
                                      ),
                                      child: Text('フィードに掲載中'),
                                    ),
                                  FilledButton.tonal(
                                    onPressed: _busy || _loading
                                        ? null
                                        : () => _open(job),
                                    child: const Text('結果を見る'),
                                  ),
                                  const SizedBox(height: 8),
                                  OutlinedButton(
                                    onPressed: _busy || _loading
                                        ? null
                                        : () => _setPublished(job),
                                    child: Text(
                                      _busyJob == job.id
                                          ? '状態を確認しています'
                                          : job.published
                                          ? '掲載を取り下げる'
                                          : 'フィードに掲載',
                                    ),
                                  ),
                                  const SizedBox(height: 8),
                                  const Text('リンクを知っている人は、掲載前や取り下げ後も結果を開けます。'),
                                ],
                                if (_jobErrors[job.id] != null)
                                  _errorText(_jobErrors[job.id]!),
                              ],
                            ),
                          ),
                        ),
                    ],
                  ],
                ),
              ),
            ),
          ),
  );
}
