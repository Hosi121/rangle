import 'package:flutter/material.dart';

import '../models/clip.dart';
import '../widgets/cheer_embers.dart';
import '../sharing/share_clip.dart';
import '../sharing/share_sheet.dart';
import 'account_controller.dart';
import 'account_dialog.dart';
import 'comments_sheet.dart';
import 'community_api.dart';

class ClipActions extends StatefulWidget {
  const ClipActions({
    required this.clip,
    required this.active,
    required this.account,
    required this.share,
    required this.compact,
    required this.sheetWidth,
    this.onCheer,
    this.cheers = 0,
    this.cheerPulse = 0,
    this.showCommentsButton = true,
    super.key,
  });
  final Clip clip;
  final bool active, compact;
  final double sheetWidth;
  final AccountController? account;
  final VoidCallback? onCheer;
  final int cheers;
  final int cheerPulse;
  final bool showCommentsButton;
  final ClipShareAction share;
  @override
  State<ClipActions> createState() => _ClipActionsState();
}

class _ClipActionsState extends State<ClipActions> {
  Engagement _value = const Engagement();
  bool _pending = false;
  String? _userId;
  int _requestVersion = 0;
  @override
  void initState() {
    super.initState();
    widget.account?.addListener(_accountChanged);
    _userId = widget.account?.user?.id;
    if (widget.active) _reload();
  }

  @override
  void didUpdateWidget(ClipActions old) {
    super.didUpdateWidget(old);
    if (widget.active && !old.active) _reload();
  }

  @override
  void dispose() {
    widget.account?.removeListener(_accountChanged);
    super.dispose();
  }

  void _accountChanged() {
    final id = widget.account?.user?.id;
    if (id != _userId) {
      _userId = id;
      if (widget.active) _reload();
    }
  }

  Future<void> _reload() async {
    final account = widget.account;
    if (account == null) return;
    final version = ++_requestVersion;
    try {
      final value = await account.api.engagement(widget.clip.id);
      if (mounted && version == _requestVersion) setState(() => _value = value);
    } catch (_) {
      /* Media remains usable while interactions are unavailable. */
    }
  }

  void _message(String value) {
    if (mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(value)));
    }
  }

  Future<void> _like() async {
    final account = widget.account;
    if (account?.user == null) {
      final signedIn = await showAccountDialog(context, account);
      if (!signedIn || !mounted) return;
    }
    if (account == null || !mounted) return;
    final previous = _value;
    final liked = !previous.liked;
    _requestVersion++;
    setState(() {
      _pending = true;
      _value = Engagement(
        likes: (previous.likes + (liked ? 1 : -1)).clamp(0, 1 << 53).toInt(),
        comments: previous.comments,
        liked: liked,
      );
    });
    try {
      await account.api.like(widget.clip.id, liked);
      await _reload();
    } catch (e) {
      if (e is CommunityFailure && e.code == 'not_authenticated') {
        account.expired();
      }
      if (mounted) setState(() => _value = previous);
      _message(communityMessage(e));
    } finally {
      if (mounted) setState(() => _pending = false);
    }
  }

  Future<void> _share() => showClipShare(
    context,
    clip: widget.clip,
    share: widget.share,
    width: widget.sheetWidth,
  );

  @override
  Widget build(BuildContext context) {
    Widget action(
      String label,
      IconData icon,
      VoidCallback? tap, {
      String? count,
      bool selected = false,
    }) => Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        IconButton.filledTonal(
          tooltip: label,
          onPressed: tap,
          isSelected: selected,
          style: IconButton.styleFrom(
            backgroundColor: Colors.black.withValues(alpha: .5),
            foregroundColor: selected ? const Color(0xFFFF954A) : Colors.white,
          ),
          icon: Icon(icon, size: 28),
        ),
        if (!widget.compact)
          Text(count ?? label, style: Theme.of(context).textTheme.labelSmall),
      ],
    );
    final actions = [
      if (widget.onCheer != null)
        CheerEmbers(
          trigger: widget.cheerPulse,
          child: action(
            '声援を送る（何度でも押せます）',
            Icons.local_fire_department,
            widget.onCheer,
            count: widget.cheers.toString(),
            selected: true,
          ),
        )
      else
        action(
          _value.liked ? 'いいねを取り消す' : 'いいね',
          _value.liked ? Icons.favorite : Icons.favorite_border,
          _pending ? null : _like,
          count: _value.likes.toString(),
          selected: _value.liked,
        ),
      if (widget.showCommentsButton)
        action('コメント', Icons.chat_bubble_outline, () async {
          await showComments(context, widget.clip.id, widget.account);
          await _reload();
        }, count: _value.comments.toString()),
      action('共有', Icons.ios_share, _share),
    ];
    return widget.compact
        ? Row(mainAxisSize: MainAxisSize.min, children: actions)
        : Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (var i = 0; i < actions.length; i++) ...[
                if (i > 0) const SizedBox(height: 12),
                actions[i],
              ],
            ],
          );
  }
}
