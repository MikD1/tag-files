using MediatR;

namespace TagFilesService.Library.Contracts;

public record GetSimilarLibraryItemsRequest(
    uint Id)
    : IRequest<List<LibraryItemDto>>;