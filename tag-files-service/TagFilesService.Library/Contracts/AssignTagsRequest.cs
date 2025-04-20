using MediatR;

namespace TagFilesService.Library.Contracts;

public record AssignTagsRequest(
    uint FileId,
    List<string> Tags)
    : IRequest<LibraryItemDto>;