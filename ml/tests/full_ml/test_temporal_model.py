"""Full ML profile: requires Torch and gsplat, and fails if either is absent.

Keep the NumPy-only archive/codec/publication CI independent of the training
runtime. Run this suite explicitly with the complete trainer image.
"""

import unittest

import numpy as np
from rangle_training.packing import pack_temporal, unpack_temporal
from test_temporal import model_fixture, temporal_fixture


class TemporalGradientTests(unittest.TestCase):
    def test_time_changes_position_opacity_and_every_temporal_gradient(self):
        import torch
        from rangle_training.temporal import initialize_parameters, temporal_values

        values = temporal_fixture()
        params, _, _ = initialize_parameters(
            values["xyz"],
            values["rgb"],
            np.full(100, 0.01),
            values["frame_times"],
            "cpu",
        )
        with torch.no_grad():
            params["velocities"][:, 0] = 0.5
        first, alpha_first = temporal_values(params, 0.0)
        last, alpha_last = temporal_values(params, 0.08)
        self.assertGreater(float((last - first).abs().sum()), 0)
        self.assertTrue(bool((alpha_first > 0.08).all()))
        self.assertTrue(bool((alpha_last > 0.08).all()))
        _, midpoint = temporal_values(params, 0.04)
        self.assertGreater(float(midpoint.mean()), float(alpha_first.mean()))
        # A shared loss from two times backpropagates into the same primitive set.
        (
            first.square().mean()
            + alpha_first.mean()
            + 2 * last.square().mean()
            + 0.5 * alpha_last.mean()
        ).backward()
        for key in ("means", "velocities", "centre_times", "log_sigmas", "opacities"):
            with self.subTest(key=key):
                self.assertIsNotNone(params[key].grad)
                self.assertTrue(bool(torch.isfinite(params[key].grad).all()))
                self.assertGreater(float(params[key].grad.abs().sum()), 0)

    def test_gsplat_topology_updates_keep_temporal_rows_and_optimizers_aligned(self):
        import torch
        from gsplat import DefaultStrategy
        from gsplat.strategy.ops import duplicate, remove, split
        from rangle_training.temporal import initialize_parameters

        values = temporal_fixture()
        params, optimizers, _ = initialize_parameters(
            values["xyz"][:3],
            values["rgb"][:3],
            np.full(3, 0.01),
            values["frame_times"],
            "cpu",
        )
        with torch.no_grad():
            params["centre_times"][:] = torch.tensor([0.01, 0.02, 0.03])
            params["velocities"][:, 0] = torch.tensor([1, 2, 3])
        DefaultStrategy().check_sanity(params, optimizers)
        # Populate Adam moments before topology changes, as actual training does.
        sum(param.sum() for param in params.values()).backward()
        for optimizer in optimizers.values():
            optimizer.step()
            optimizer.zero_grad()
        before = {key: value.detach().clone() for key, value in params.items()}
        duplicate(params, optimizers, {}, torch.tensor([False, True, False]))
        remove(params, optimizers, {}, torch.tensor([True, False, False, False]))
        for key, value in params.items():
            np.testing.assert_allclose(value.detach(), before[key][[1, 2, 1]])
            self.assertIs(optimizers[key].param_groups[0]["params"][0], value)
            self.assertEqual(optimizers[key].state[value]["exp_avg"].shape, value.shape)
        DefaultStrategy().check_sanity(params, optimizers)
        before_split = {key: value.detach().clone() for key, value in params.items()}
        split(params, optimizers, {}, torch.tensor([False, True, False]))
        for key, value in params.items():
            if key not in ("means", "scales"):
                torch.testing.assert_close(value, before_split[key][[0, 2, 1, 1]])
            self.assertIs(optimizers[key].param_groups[0]["params"][0], value)
            self.assertEqual(optimizers[key].state[value]["exp_avg"].shape, value.shape)
        DefaultStrategy().check_sanity(params, optimizers)

    def test_export_evaluation_matches_shader_and_round_even_alpha(self):
        import torch
        from rangle_training.temporal import stored_values

        arrays = tuple(
            torch.as_tensor(array)
            for array in unpack_temporal(
                pack_temporal(*model_fixture(), frame_count=3, frame_rate=25)
            )
        )
        position, _, _, _, opacity = stored_values(arrays, torch.tensor(0.08))
        np.testing.assert_allclose(
            position.numpy(),
            model_fixture()[0]
            + model_fixture()[5] * (np.float32(0.08) - model_fixture()[6])[:, None],
        )
        peak, centre, sigma = arrays[4], arrays[6], arrays[7]
        expected = (
            torch.round(255 * peak * torch.exp(-0.5 * ((0.08 - centre) / sigma) ** 2))
            / 255
        )
        torch.testing.assert_close(opacity, expected)
        self.assertEqual(float(opacity[1]), 0)

    def test_export_alpha_rounds_half_integer_ties_to_even(self):
        import torch
        from rangle_training.temporal import stored_values

        arrays = [torch.as_tensor(array) for array in model_fixture()]
        arrays[4] = torch.tensor([0.5 / 255, 1.5 / 255], dtype=torch.float32)
        arrays[6] = torch.zeros(2)
        alpha = stored_values(arrays, torch.tensor(0.0))[4]
        torch.testing.assert_close(alpha, torch.tensor([0, 2 / 255]))


if __name__ == "__main__":
    unittest.main()
