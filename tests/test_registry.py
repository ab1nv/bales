"""Tests for ModelRegistry."""

import pytest
import pytest_asyncio

pytestmark = pytest.mark.asyncio


class TestModelRegistry:
    @pytest_asyncio.fixture
    async def registry(self):
        from models.registry import ModelRegistry

        return ModelRegistry()

    @pytest_asyncio.fixture
    async def stub(self):
        from models.stub_model import StubModel

        return StubModel()

    async def test_register_and_get(self, registry, stub):
        await registry.register("test_v1", stub)
        fetched = registry.get("test_v1")
        assert fetched is stub

    async def test_get_unregistered_raises(self, registry):
        with pytest.raises(KeyError):
            registry.get("missing")

    async def test_list_models(self, registry, stub):
        await registry.register("a", stub)
        await registry.register("b", stub)
        assert sorted(registry.list_models()) == ["a", "b"]

    async def test_contains(self, registry, stub):
        await registry.register("exists", stub)
        assert "exists" in registry
        assert "missing" not in registry

    async def test_register_overwrites(self, registry, stub):
        await registry.register("same", stub)
        await registry.register("same", stub)
        assert registry.list_models() == ["same"]

    async def test_hot_swap_replaces_model(self, registry, stub):
        from models.stub_model import StubModel

        await registry.register("swap_me", stub)
        new_model = StubModel()
        await registry.hot_swap("swap_me", new_model)
        assert registry.get("swap_me") is new_model

    async def test_hot_swap_unregistered_raises(self, registry, stub):
        with pytest.raises(KeyError):
            await registry.hot_swap("missing", stub)

    async def test_register_non_basemodel_raises(self, registry):
        with pytest.raises(TypeError):
            await registry.register("bad", "not_a_model")
