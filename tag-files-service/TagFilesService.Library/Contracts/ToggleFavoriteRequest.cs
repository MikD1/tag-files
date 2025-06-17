using MediatR;

namespace TagFilesService.Library.Contracts;

public record ToggleFavoriteRequest(uint Id) : IRequest;