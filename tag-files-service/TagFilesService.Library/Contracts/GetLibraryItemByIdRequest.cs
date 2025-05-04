using MediatR;

namespace TagFilesService.Library.Contracts;

public record GetLibraryItemByIdRequest(
    uint Id)
    : IRequest<LibraryItemDto?>;