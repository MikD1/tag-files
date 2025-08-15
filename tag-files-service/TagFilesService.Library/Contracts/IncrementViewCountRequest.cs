using MediatR;

namespace TagFilesService.Library.Contracts;

public record IncrementViewCountRequest(
    uint LibraryItemId)
    : IRequest;