using MediatR;

namespace TagFilesService.Library.Contracts;

public record AssignTagsRequest(
    List<uint> ItemsList,
    List<string> Tags)
    : IRequest<List<LibraryItemDto>>;