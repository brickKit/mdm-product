from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Aggregate(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    AGGREGATE_UNSPECIFIED: _ClassVar[Aggregate]
    AGGREGATE_PRODUCT: _ClassVar[Aggregate]
    AGGREGATE_CATEGORY: _ClassVar[Aggregate]

class ReadState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    READ_STATE_UNSPECIFIED: _ClassVar[ReadState]
    READ_STATE_EXISTS: _ClassVar[ReadState]
    READ_STATE_TOMBSTONE: _ClassVar[ReadState]
    READ_STATE_PURGED: _ClassVar[ReadState]

class Purpose(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PURPOSE_UNSPECIFIED: _ClassVar[Purpose]
    PURPOSE_SELL: _ClassVar[Purpose]
    PURPOSE_PURCHASE: _ClassVar[Purpose]

class Resolution(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RESOLUTION_UNSPECIFIED: _ClassVar[Resolution]
    RESOLUTION_CURRENT: _ClassVar[Resolution]
    RESOLUTION_ALIAS: _ClassVar[Resolution]
    RESOLUTION_TOMBSTONE: _ClassVar[Resolution]
    RESOLUTION_NOT_FOUND: _ClassVar[Resolution]
AGGREGATE_UNSPECIFIED: Aggregate
AGGREGATE_PRODUCT: Aggregate
AGGREGATE_CATEGORY: Aggregate
READ_STATE_UNSPECIFIED: ReadState
READ_STATE_EXISTS: ReadState
READ_STATE_TOMBSTONE: ReadState
READ_STATE_PURGED: ReadState
PURPOSE_UNSPECIFIED: Purpose
PURPOSE_SELL: Purpose
PURPOSE_PURCHASE: Purpose
RESOLUTION_UNSPECIFIED: Resolution
RESOLUTION_CURRENT: Resolution
RESOLUTION_ALIAS: Resolution
RESOLUTION_TOMBSTONE: Resolution
RESOLUTION_NOT_FOUND: Resolution

class Stamp(_message.Message):
    __slots__ = ("deployment", "instance", "epoch", "positions")
    DEPLOYMENT_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_FIELD_NUMBER: _ClassVar[int]
    EPOCH_FIELD_NUMBER: _ClassVar[int]
    POSITIONS_FIELD_NUMBER: _ClassVar[int]
    deployment: str
    instance: str
    epoch: str
    positions: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, deployment: _Optional[str] = ..., instance: _Optional[str] = ..., epoch: _Optional[str] = ..., positions: _Optional[_Iterable[int]] = ...) -> None: ...

class Record(_message.Message):
    __slots__ = ("id", "version", "data")
    ID_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    id: str
    version: int
    data: bytes
    def __init__(self, id: _Optional[str] = ..., version: _Optional[int] = ..., data: _Optional[bytes] = ...) -> None: ...

class SnapshotRequest(_message.Message):
    __slots__ = ("aggregate", "cursor", "limit")
    AGGREGATE_FIELD_NUMBER: _ClassVar[int]
    CURSOR_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    aggregate: Aggregate
    cursor: str
    limit: int
    def __init__(self, aggregate: _Optional[_Union[Aggregate, str]] = ..., cursor: _Optional[str] = ..., limit: _Optional[int] = ...) -> None: ...

class SnapshotResponse(_message.Message):
    __slots__ = ("records", "next_cursor", "stamp")
    RECORDS_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    STAMP_FIELD_NUMBER: _ClassVar[int]
    records: _containers.RepeatedCompositeFieldContainer[Record]
    next_cursor: str
    stamp: Stamp
    def __init__(self, records: _Optional[_Iterable[_Union[Record, _Mapping]]] = ..., next_cursor: _Optional[str] = ..., stamp: _Optional[_Union[Stamp, _Mapping]] = ...) -> None: ...

class ReadRequest(_message.Message):
    __slots__ = ("aggregate", "ids")
    AGGREGATE_FIELD_NUMBER: _ClassVar[int]
    IDS_FIELD_NUMBER: _ClassVar[int]
    aggregate: Aggregate
    ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, aggregate: _Optional[_Union[Aggregate, str]] = ..., ids: _Optional[_Iterable[str]] = ...) -> None: ...

class ReadResult(_message.Message):
    __slots__ = ("id", "state", "version", "data")
    ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    id: str
    state: ReadState
    version: int
    data: bytes
    def __init__(self, id: _Optional[str] = ..., state: _Optional[_Union[ReadState, str]] = ..., version: _Optional[int] = ..., data: _Optional[bytes] = ...) -> None: ...

class ReadResponse(_message.Message):
    __slots__ = ("results", "stamp")
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    STAMP_FIELD_NUMBER: _ClassVar[int]
    results: _containers.RepeatedCompositeFieldContainer[ReadResult]
    stamp: Stamp
    def __init__(self, results: _Optional[_Iterable[_Union[ReadResult, _Mapping]]] = ..., stamp: _Optional[_Union[Stamp, _Mapping]] = ...) -> None: ...

class CheckEligibilityRequest(_message.Message):
    __slots__ = ("product_id", "legal_entity_id", "purpose")
    PRODUCT_ID_FIELD_NUMBER: _ClassVar[int]
    LEGAL_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    PURPOSE_FIELD_NUMBER: _ClassVar[int]
    product_id: str
    legal_entity_id: str
    purpose: Purpose
    def __init__(self, product_id: _Optional[str] = ..., legal_entity_id: _Optional[str] = ..., purpose: _Optional[_Union[Purpose, str]] = ...) -> None: ...

class CheckEligibilityResponse(_message.Message):
    __slots__ = ("eligible", "reason", "product_version", "stocked", "evaluated_at")
    ELIGIBLE_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    PRODUCT_VERSION_FIELD_NUMBER: _ClassVar[int]
    STOCKED_FIELD_NUMBER: _ClassVar[int]
    EVALUATED_AT_FIELD_NUMBER: _ClassVar[int]
    eligible: bool
    reason: str
    product_version: int
    stocked: bool
    evaluated_at: str
    def __init__(self, eligible: _Optional[bool] = ..., reason: _Optional[str] = ..., product_version: _Optional[int] = ..., stocked: _Optional[bool] = ..., evaluated_at: _Optional[str] = ...) -> None: ...

class ResolveRequest(_message.Message):
    __slots__ = ("ids",)
    IDS_FIELD_NUMBER: _ClassVar[int]
    ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, ids: _Optional[_Iterable[str]] = ...) -> None: ...

class Resolved(_message.Message):
    __slots__ = ("id", "resolution", "version", "data", "survivor_id")
    ID_FIELD_NUMBER: _ClassVar[int]
    RESOLUTION_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    SURVIVOR_ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    resolution: Resolution
    version: int
    data: bytes
    survivor_id: str
    def __init__(self, id: _Optional[str] = ..., resolution: _Optional[_Union[Resolution, str]] = ..., version: _Optional[int] = ..., data: _Optional[bytes] = ..., survivor_id: _Optional[str] = ...) -> None: ...

class ResolveResponse(_message.Message):
    __slots__ = ("results",)
    RESULTS_FIELD_NUMBER: _ClassVar[int]
    results: _containers.RepeatedCompositeFieldContainer[Resolved]
    def __init__(self, results: _Optional[_Iterable[_Union[Resolved, _Mapping]]] = ...) -> None: ...

class ConvertRequest(_message.Message):
    __slots__ = ("product_id", "quantity", "from_unit", "to_unit")
    PRODUCT_ID_FIELD_NUMBER: _ClassVar[int]
    QUANTITY_FIELD_NUMBER: _ClassVar[int]
    FROM_UNIT_FIELD_NUMBER: _ClassVar[int]
    TO_UNIT_FIELD_NUMBER: _ClassVar[int]
    product_id: str
    quantity: str
    from_unit: str
    to_unit: str
    def __init__(self, product_id: _Optional[str] = ..., quantity: _Optional[str] = ..., from_unit: _Optional[str] = ..., to_unit: _Optional[str] = ...) -> None: ...

class ConvertResponse(_message.Message):
    __slots__ = ("quantity", "factor", "source", "product_version")
    QUANTITY_FIELD_NUMBER: _ClassVar[int]
    FACTOR_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    PRODUCT_VERSION_FIELD_NUMBER: _ClassVar[int]
    quantity: str
    factor: str
    source: str
    product_version: int
    def __init__(self, quantity: _Optional[str] = ..., factor: _Optional[str] = ..., source: _Optional[str] = ..., product_version: _Optional[int] = ...) -> None: ...
