using MediatR;

namespace TagFilesService.Library.Contracts;

public record DeleteLibraryItemRequest(
    uint Id)
    : IRequest;