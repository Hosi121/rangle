"""Complete-ML CPU checks: pinned OpenCV 4.13 plus Torch, no CUDA or network."""

import math
import time
import unittest
from types import SimpleNamespace

import cv2
import numpy as np
import torch
from rangle_live.model import IncrementalModel
from rangle_live.window import Frame, Window
from rangle_training.flow import (
    _projected_loss,
    forward_backward_confidence,
    prepare_observed_flow,
)


def parameters(count=1):
    return torch.nn.ParameterDict(
        {
            "means": torch.nn.Parameter(torch.tensor([[0.0, 0, 2]]).repeat(count, 1)),
            "velocities": torch.nn.Parameter(torch.zeros(count, 3)),
            "centre_times": torch.nn.Parameter(torch.zeros(count)),
            "opacities": torch.nn.Parameter(torch.zeros(count)),
            "log_sigmas": torch.nn.Parameter(torch.zeros(count)),
        }
    )


def constant_observation(dx, dy):
    value = torch.ones(1, 3, 64, 96)
    value[:, 0], value[:, 1] = dx, dy
    return value


class ObservedFlowTests(unittest.TestCase):
    K = torch.tensor([[100.0, 0, 48], [0, 100.0, 32], [0, 0, 1]])

    def test_pixel_displacement_huber_scale_and_velocity_only_gradient(self):
        p = parameters()
        view, K = torch.eye(4, requires_grad=True), self.K.clone().requires_grad_()
        loss, valid, samples = _projected_loss(
            p, 0, 0.04, view, K, constant_observation(2, 0)
        )
        self.assertAlmostEqual(float(loss.detach()), 0.01 * 1.5 / 2 / 96, places=10)
        self.assertEqual((float(valid), samples), (1, 1))
        loss.backward()
        self.assertIsNone(view.grad)
        self.assertIsNone(K.grad)
        self.assertLess(float(p["velocities"].grad[0, 0]), 0)
        for name in ("means", "centre_times", "opacities", "log_sigmas"):
            self.assertIsNone(p[name].grad)
        with torch.no_grad():
            p["velocities"][0, 0] = 1
        exact = _projected_loss(
            p, 0, 0.04, torch.eye(4), self.K, constant_observation(2, 0)
        )[0]
        self.assertEqual(float(exact.detach()), 0)

    def test_world_to_camera_rotation_y_down_and_no_valid_observations(self):
        p = parameters()
        with torch.no_grad():
            p["velocities"][0, 0] = 1
        view = torch.tensor(
            [[0.0, -1, 0, 0.1], [1, 0, 0, 0], [0, 0, 1, 0], [0, 0, 0, 1]]
        )
        loss, valid, _ = _projected_loss(
            p, 0, 0.04, view, self.K, constant_observation(0, 2)
        )
        self.assertEqual(float(loss.detach()), 0)
        self.assertEqual(float(valid), 1)
        with torch.no_grad():
            p["means"][:, 2] = -2
        loss, valid, _ = _projected_loss(
            p, 0, 0.04, view, self.K, constant_observation(0, 2)
        )
        loss.backward()
        self.assertTrue(torch.isfinite(loss))
        self.assertEqual((float(loss.detach()), float(valid)), (0, 0))
        self.assertEqual(float(p["velocities"].grad.abs().sum()), 0)

    def test_forward_backward_warp_bounds_and_one_pixel_threshold(self):
        forward = np.zeros((32, 48, 2), np.float32)
        forward[..., 0] = 2
        backward = -forward
        mask = forward_backward_confidence(forward, backward)
        self.assertTrue(mask[:, :-2].all())
        self.assertFalse(mask[:, -2:].any())
        self.assertFalse(
            forward_backward_confidence(forward, np.zeros_like(backward)).any()
        )
        backward[..., 0] = -1
        self.assertTrue(forward_backward_confidence(forward, backward)[:, :-2].all())

    def test_deterministic_sampling_survives_topology_count_changes(self):
        state = torch.random.get_rng_state().clone()
        for count in (5000, 1, 7000):
            loss, _, samples = _projected_loss(
                parameters(count),
                0,
                0.04,
                torch.eye(4),
                self.K,
                constant_observation(2, 0),
            )
            self.assertEqual(samples, math.ceil(count / math.ceil(count / 4096)))
            self.assertLessEqual(samples, 4096)
            self.assertTrue(torch.isfinite(loss))
        torch.testing.assert_close(torch.random.get_rng_state(), state, rtol=0, atol=0)

    def test_real_dis_preparation_uses_only_train_views_and_arrived_pairs(self):
        rng = np.random.default_rng(4)
        image = cv2.GaussianBlur(rng.integers(0, 256, (64, 96, 3), np.uint8), (3, 3), 0)
        images = np.zeros((3, 12, 64, 96, 3), np.uint8)
        for frame in range(3):
            for view in (1, 11):
                images[frame, view] = np.roll(image, frame * 2, axis=1)
        options = {
            "view_ids": list(range(12)),
            "train_indices": [i for i in range(12) if i not in (0, 10)],
            "heldout_indices": [0, 10],
            "latest_arrived_time_seconds": 0.08,
        }
        state = torch.random.get_rng_state().clone()
        previous = cv2.getNumThreads()
        flow = prepare_observed_flow(images, [0.0, 0.04, 0.08], [0, 1, 2], **options)
        self.assertEqual(cv2.getNumThreads(), previous)
        self.assertEqual(set(flow.fields), {(f, v) for f in (1, 2) for v in (1, 11)})
        observed = flow.fields[1, 1][0, :, 12:-12, 12:-12]
        self.assertAlmostEqual(float(observed[0].mean()), 2, delta=0.6)
        self.assertGreater(float(observed[2].mean()), 0.5)
        images[:, [0, 10]] = rng.integers(0, 256, images[:, [0, 10]].shape, np.uint8)
        other = prepare_observed_flow(images, [0.0, 0.04, 0.08], [0, 1, 2], **options)
        for key in flow.fields:
            torch.testing.assert_close(
                flow.fields[key], other.fields[key], rtol=0, atol=0
            )
        torch.testing.assert_close(torch.random.get_rng_state(), state, rtol=0, atol=0)
        window = SimpleNamespace(
            frames=[SimpleNamespace(number=i, pts=i / 25) for i in range(3)]
        )
        flow.validate_window(window)
        with self.assertRaises(ValueError):
            flow.validate_window(SimpleNamespace(frames=window.frames[1:]))
        p = parameters()
        self.assertEqual(float(flow.loss(p, 0, 1, torch.eye(4), self.K).detach()), 0)
        self.assertEqual(float(flow.loss(p, 1, 0, torch.eye(4), self.K).detach()), 0)
        loss = flow.loss(p, 1, 1, torch.eye(4), self.K)
        loss.backward()
        self.assertEqual(flow.metrics()["flow_auxiliary_steps"], 1)
        self.assertGreater(float(p["velocities"].grad.abs().sum()), 0)

    def test_camera_ids_are_resolved_independently_of_array_order(self):
        images = np.zeros((2, 12, 64, 96, 3), np.uint8)
        ids = list(range(12))
        ids[1], ids[2] = ids[2], ids[1]
        flow = prepare_observed_flow(
            images,
            [0.0, 0.04],
            [0, 1],
            view_ids=ids,
            train_indices=[i for i in range(12) if i not in (0, 10)],
            heldout_indices=[0, 10],
            latest_arrived_time_seconds=0.04,
        )
        self.assertEqual(flow.view_indices, (2, 11))
        self.assertEqual(set(flow.fields), {(1, 2), (1, 11)})

    def test_actual_fit_default_and_explicit_none_preserve_rng_and_parameters(self):
        from rangle_training.temporal import initialize_parameters

        def model():
            result = object.__new__(IncrementalModel)
            result.torch, result.device = torch, "cpu"
            result.rig = SimpleNamespace(
                width=96,
                height=64,
                fps=25,
                train=np.array([1, 11]),
                heldout=np.array([0, 10]),
            )
            result.window_start, result.total_steps, result.duration = None, 0, 0.08
            result.rng = np.random.default_rng(0)
            result.params, result.optimizers, result.rates = initialize_parameters(
                np.array([[0, 0, 2]], np.float32),
                np.array([[128, 128, 128]], np.uint8),
                np.array([0.1]),
                np.array([0, 0.04, 0.08]),
                "cpu",
                quality_profile="local-lifetime",
            )
            result.strategy = SimpleNamespace(
                step_pre_backward=lambda *_a: None,
                step_post_backward=lambda *_a, **_k: None,
            )
            result.strategy_state = {}
            result.views = torch.eye(4)[None].repeat(12, 1, 1)
            result.Ks = self.K[None].repeat(12, 1, 1)
            result.ssim = lambda a, b: 1 - (a - b).square().mean()
            result.render = lambda *_a: (
                result.params["colors"].sigmoid().view(1, 1, 1, 3).expand(1, 64, 96, 3),
                None,
                {},
            )
            return result

        frames = tuple(
            Frame(i, i / 25, 0, 0, np.zeros((12, 64, 96, 3), np.uint8))
            for i in range(3)
        )
        window = Window(frames, 1)
        implicit, explicit = model(), model()
        original = implicit.fit(window, 10, time.monotonic() + 30)
        same = explicit.fit(window, 10, time.monotonic() + 30, offline_auxiliary=None)
        self.assertEqual(
            implicit.rng.bit_generator.state, explicit.rng.bit_generator.state
        )
        for name in implicit.params:
            torch.testing.assert_close(
                implicit.params[name], explicit.params[name], rtol=0, atol=0
            )
        self.assertEqual(
            original["training_frame_sample_counts"],
            same["training_frame_sample_counts"],
        )
        self.assertFalse(any(key.startswith("flow_") for key in original))
        observed = prepare_observed_flow(
            [f.images for f in frames],
            [f.pts for f in frames],
            [f.number for f in frames],
            view_ids=list(range(12)),
            train_indices=[i for i in range(12) if i not in (0, 10)],
            heldout_indices=[0, 10],
            latest_arrived_time_seconds=window.end,
        )
        candidate = model()
        measured = candidate.fit(
            window, 10, time.monotonic() + 30, offline_auxiliary=observed
        )
        self.assertEqual(
            candidate.rng.bit_generator.state, implicit.rng.bit_generator.state
        )
        self.assertGreater(measured["flow_auxiliary_steps"], 0)
        self.assertLessEqual(measured["flow_auxiliary_steps"], 10)
        self.assertEqual(measured["flow_latest_observed_time_seconds"], window.end)


if __name__ == "__main__":
    unittest.main()
