"""Explicit complete-ML CPU tests; lightweight contract CI does not import Torch."""

import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

import cv2
import numpy as np
import torch
from rangle_live.model import IncrementalModel, advance_local_window
from rangle_live.window import Frame, Window
from rangle_training.temporal import initialize_parameters, temporal_values


class PersistentStateTests(unittest.TestCase):
    def test_diagnostics_are_local_packed_rgb_at_the_final_window_times(self):
        stored = object()
        calls = []

        def render(frame, view, arrays):
            self.assertIs(arrays, stored)
            calls.append((frame, view))
            return (torch.tensor([1.0, 0.25, 0.0]).expand(1, 64, 96, 3),)

        state = SimpleNamespace(
            torch=torch,
            rig=SimpleNamespace(heldout=[3]),
            render=render,
            last_stored=stored,
        )
        window = Window(tuple(Frame(n, n / 25, 0, 0, None) for n in range(230, 250)), 1)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "diagnostics"
            IncrementalModel.write_diagnostics(state, root, window)
            self.assertEqual(calls, [(0, 3), (10, 3), (19, 3)])
            self.assertEqual(
                {p.name for p in root.iterdir()},
                {f"heldout-03-frame-{n:06d}.webp" for n in (230, 240, 249)},
            )
            bgr = cv2.imread(str(root / "heldout-03-frame-000249.webp"))
            self.assertEqual(bgr.shape, (64, 96, 3))
            self.assertGreater(
                float(bgr[..., 2].mean()), float(bgr[..., 0].mean()) + 200
            )

    def test_window_prediction_preserves_geometry_and_adam_with_local_gate_reset(self):
        p, optimizers, _ = initialize_parameters(
            np.array([[0, 0, 1], [0.1, 0.2, 1]], np.float32),
            np.full((2, 3), 0.5, np.float32),
            np.full(2, 0.01),
            np.array([0, 0.04, 0.08]),
            "cpu",
            quality_profile="local-lifetime",
        )
        # Give each Adam optimizer actual state, then use known nonzero motion.
        sum(value.square().sum() for value in p.values()).backward()
        for optimizer in optimizers.values():
            optimizer.step()
            optimizer.zero_grad(set_to_none=True)
        with torch.no_grad():
            p["velocities"].copy_(torch.tensor([[0.1, 0.2, 0], [-0.1, 0, 0.2]]))
        identities = {key: id(value) for key, value in p.items()}
        moments = {
            key: {
                name: value.clone() for name, value in optimizer.state[p[key]].items()
            }
            for key, optimizer in optimizers.items()
        }
        centres, sigmas = p["centre_times"].clone(), p["log_sigmas"].clone()
        delta = 0.8
        old_positions = [
            temporal_values(p, t + delta)[0].detach().clone() for t in (0, 0.03, 0.08)
        ]
        local_opacities = [
            temporal_values(p, t)[1].detach().clone() for t in (0, 0.03, 0.08)
        ]
        physical_future_gate = temporal_values(p, delta)[1].detach().clone()
        advance_local_window(p, delta)
        for t, position, opacity in zip(
            (0, 0.03, 0.08), old_positions, local_opacities, strict=True
        ):
            current_position, current_opacity = temporal_values(p, t)
            torch.testing.assert_close(current_position, position)
            torch.testing.assert_close(current_opacity, opacity)
        self.assertFalse(torch.allclose(temporal_values(p, 0)[1], physical_future_gate))
        torch.testing.assert_close(p["centre_times"], centres, rtol=0, atol=0)
        torch.testing.assert_close(p["log_sigmas"], sigmas, rtol=0, atol=0)
        self.assertEqual({key: id(value) for key, value in p.items()}, identities)
        for key, optimizer in optimizers.items():
            self.assertIs(optimizer.param_groups[0]["params"][0], p[key])
            for name, value in optimizer.state[p[key]].items():
                torch.testing.assert_close(value, moments[key][name], rtol=0, atol=0)
        for invalid in (-0.1, float("nan"), float("inf")):
            with self.assertRaises(ValueError):
                advance_local_window(p, invalid)


if __name__ == "__main__":
    unittest.main()
