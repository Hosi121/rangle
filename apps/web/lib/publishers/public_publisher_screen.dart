import 'dart:async';

import 'package:flutter/material.dart';

import '../community/account_controller.dart';
import '../community/account_dialog.dart';
import '../community/community_api.dart';
import '../data/clip_repository.dart';
import '../models/clip.dart';
import '../models/publisher_account.dart';
import '../widgets/rangle_header.dart';
import '../widgets/viewer_viewport.dart';
import 'public_publisher_api.dart';

class PublicPublisherScreen extends StatefulWidget {
  const PublicPublisherScreen({
    required this.id,
    required this.account,
    super.key,
  });
  final String id;
  final AccountController account;
  @override
  State<PublicPublisherScreen> createState() => _PublicPublisherScreenState();
}

class _PublicPublisherScreenState extends State<PublicPublisherScreen> {
  PublisherAccount? _publisher;
  final _clips = <Clip>[];
  String? _cursor, _error, _clipError, _followError, _viewer;
  bool _loading = true,
      _clipsLoading = false,
      _more = true,
      _followBusy = false;
  bool? _subscribed;
  int _followEpoch = 0;

  @override
  void initState() {
    super.initState();
    _viewer = widget.account.user?.id;
    widget.account.addListener(_accountChanged);
    unawaited(_load());
  }

  @override
  void dispose() {
    _followEpoch++;
    widget.account.removeListener(_accountChanged);
    super.dispose();
  }

  void _accountChanged() {
    if (_viewer == widget.account.user?.id) return;
    setState(() {
      _viewer = widget.account.user?.id;
      _followEpoch++;
      _subscribed = null;
      _followBusy = false;
      _followError = null;
    });
    if (_publisher != null) unawaited(_loadSubscription());
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final publisher = await widget.account.api.publicPublisher(widget.id);
      if (!mounted) return;
      setState(() => _publisher = publisher);
      unawaited(_loadSubscription());
      await _loadClips();
    } catch (e) {
      if (mounted) setState(() => _error = communityMessage(e));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _loadClips() async {
    if (_clipsLoading || !_more) return;
    setState(() {
      _clipsLoading = true;
      _clipError = null;
    });
    try {
      final page = await widget.account.api.publisherClips(
        widget.id,
        cursor: _cursor,
      );
      if (!mounted) return;
      if (page.nextCursor != null && page.nextCursor == _cursor) {
        throw const FormatException('Publisher cursor did not advance.');
      }
      setState(() {
        final ids = _clips.map((clip) => clip.id).toSet();
        _clips.addAll(page.clips.where((clip) => ids.add(clip.id)));
        _cursor = page.nextCursor;
        _more = _cursor != null;
      });
    } on StaleClipCursor {
      if (mounted) {
        setState(() {
          _clips.clear();
          _cursor = null;
          _more = true;
          _clipError = '動画一覧が更新されました。再読み込みしてください。';
        });
      }
    } catch (_) {
      if (mounted) setState(() => _clipError = '動画一覧を読み込めませんでした。');
    } finally {
      if (mounted) setState(() => _clipsLoading = false);
    }
  }

  Future<void> _loadSubscription() async {
    final viewer = _viewer, epoch = ++_followEpoch;
    if (viewer == null) return;
    setState(() {
      _followBusy = true;
      _followError = null;
    });
    try {
      final value = await widget.account.api.publisherSubscription(widget.id);
      if (mounted && epoch == _followEpoch && viewer == _viewer) {
        setState(() => _subscribed = value);
      }
    } catch (e) {
      if (mounted && epoch == _followEpoch) {
        if (e is CommunityFailure && e.code == 'not_authenticated') {
          widget.account.expired();
        }
        if (viewer == _viewer) {
          setState(() => _followError = communityMessage(e));
        }
      }
    } finally {
      if (mounted && epoch == _followEpoch) setState(() => _followBusy = false);
    }
  }

  Future<void> _toggleSubscription() async {
    if (_followBusy) return;
    if (_viewer == null) {
      await showAccountDialog(context, widget.account);
      if (!mounted || _viewer == null) return;
      await _loadSubscription();
      if (!mounted) return;
    }
    if (_subscribed == null) {
      await _loadSubscription();
      return;
    }
    final viewer = _viewer, epoch = ++_followEpoch, value = !_subscribed!;
    setState(() {
      _followBusy = true;
      _followError = null;
    });
    try {
      await widget.account.api.setPublisherSubscription(widget.id, value);
      if (mounted && epoch == _followEpoch && viewer == _viewer) {
        setState(() => _subscribed = value);
      }
    } catch (e) {
      if (mounted && epoch == _followEpoch) {
        if (e is CommunityFailure && e.code == 'not_authenticated') {
          widget.account.expired();
        }
        if (viewer == _viewer) {
          setState(() => _followError = communityMessage(e));
        }
      }
    } finally {
      if (mounted && epoch == _followEpoch) setState(() => _followBusy = false);
    }
  }

  @override
  Widget build(BuildContext context) => ViewerViewport(
    child: Scaffold(
      appBar: RangleHeader(title: '団体', onBack: () => Navigator.pop(context)),
      body: ListView(
        padding: const EdgeInsets.all(20),
        children: [
          if (_publisher case final publisher?) ...[
            Text(
              publisher.name,
              style: Theme.of(context).textTheme.headlineSmall,
            ),
            const SizedBox(height: 4),
            Text('@${publisher.id}'),
            const SizedBox(height: 16),
            FilledButton.tonalIcon(
              key: const Key('publisher-follow'),
              onPressed: _followBusy ? null : _toggleSubscription,
              icon: Icon(_subscribed == true ? Icons.check : Icons.add),
              label: Text(
                _followBusy
                    ? '確認しています'
                    : _viewer == null
                    ? 'ログインしてフォロー'
                    : _subscribed == true
                    ? 'フォロー中'
                    : _subscribed == null
                    ? 'フォロー状態を再確認'
                    : 'フォローする',
              ),
            ),
            if (_followError != null)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(
                  _followError!,
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
              ),
            const SizedBox(height: 28),
            Text('公開動画', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            for (final clip in _clips)
              ListTile(
                contentPadding: EdgeInsets.zero,
                leading: const Icon(Icons.play_circle_outline),
                title: Text(clip.title),
                subtitle: Text(clip.sport),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => Navigator.pop(context, clip.id),
              ),
            if (_clipError != null) Text(_clipError!),
            if (_clipsLoading)
              const Padding(
                padding: EdgeInsets.all(16),
                child: Text('動画一覧を読み込んでいます'),
              ),
            if (!_clipsLoading && _clipError == null && _clips.isEmpty) ...[
              const Text('公開された動画はまだありません。'),
              TextButton(
                onPressed: () => Navigator.pop(context),
                child: const Text('フィードへ戻る'),
              ),
            ],
            if (!_clipsLoading && (_more || _clipError != null))
              TextButton(
                onPressed: _loadClips,
                child: Text(_clipError != null ? '再読み込み' : 'もっと見る'),
              ),
          ] else if (_loading)
            const Text('団体を読み込んでいます'),
          if (_error != null) ...[
            Text(_error!),
            TextButton(
              onPressed: _loading ? null : _load,
              child: const Text('再読み込み'),
            ),
          ],
        ],
      ),
    ),
  );
}
